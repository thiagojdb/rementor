// Package shared serializes the selector for equivalent ingress gateways.
// Moving its owner preserves the routing computer and published policy. It
// never substitutes a gateway's own environment for the selected environment.
package shared

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"

	"github.com/thiagojdb/rementor/internal/access"
)

const SchemaVersion = 1

var (
	ErrConflict        = errors.New("shared ingress changed; reload before editing")
	ErrNotOwner        = errors.New("shared ingress changes require its current owner")
	ErrNotMember       = errors.New("computer is not a gateway for this shared ingress")
	ErrTransferPending = errors.New("shared ingress ownership transfer requires recovery or cancellation")
	ErrUnavailable     = errors.New("shared ingress has not been configured")
)

// Record is the owner-controlled selector. RoutingOwner is intentionally
// independent from Owner: moving control or an ingress gateway cannot move an
// application endpoint. Policy workspace IDs are in RoutingOwner's namespace.
type Record struct {
	SchemaVersion int             `json:"schemaVersion"`
	GroupID       string          `json:"groupId"`
	Version       uint64          `json:"version,string"`
	Epoch         uint64          `json:"epoch,string"`
	Owner         string          `json:"ownerFingerprint"`
	RoutingOwner  string          `json:"routingOwnerFingerprint"`
	Gateways      []string        `json:"gateways"`
	Policy        access.Document `json:"policy"`
	PreviousOwner string          `json:"previousOwnerFingerprint,omitempty"`
	Predecessor   string          `json:"predecessorDigest,omitempty"`
	Digest        string          `json:"digest"`
}

func ValidFingerprint(value string) bool {
	raw, err := hex.DecodeString(value)
	return err == nil && len(raw) == sha256.Size && hex.EncodeToString(raw) == value
}
func (r Record) Allows(peer string) bool {
	if peer == r.Owner || peer == r.RoutingOwner {
		return true
	}
	for _, gateway := range r.Gateways {
		if gateway == peer {
			return true
		}
	}
	return false
}
func (r Record) Clone() Record {
	r.Gateways = append([]string(nil), r.Gateways...)
	r.Policy = r.Policy.Clone()
	return r
}
func (r Record) contentDigest() (string, error) {
	r.Digest = ""
	raw, err := json.Marshal(r)
	if err != nil {
		return "", err
	}
	if len(raw) > access.MaxDocumentBytes {
		return "", errors.New("shared ingress record exceeds 1 MiB")
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}
func seal(r Record) (Record, error) {
	var err error
	r.Digest, err = r.contentDigest()
	if err == nil {
		err = r.Validate()
	}
	return r, err
}
func (r Record) Validate() error {
	if r.SchemaVersion != SchemaVersion || !ValidFingerprint(r.GroupID) || !ValidFingerprint(r.Owner) || !ValidFingerprint(r.RoutingOwner) || r.Version == 0 || r.Epoch == 0 || r.Epoch > r.Version {
		return errors.New("invalid shared ingress identity, schema or version")
	}
	if r.Version > math.MaxInt64 || len(r.Gateways) > 64 {
		return errors.New("shared ingress version or gateway limit exceeded")
	}
	seen := map[string]bool{}
	for _, gateway := range r.Gateways {
		if !ValidFingerprint(gateway) || seen[gateway] {
			return errors.New("invalid or duplicate shared ingress gateway")
		}
		seen[gateway] = true
	}
	if !seen[r.Owner] {
		return errors.New("selector owner must be a configured gateway")
	}
	if (r.Epoch == 1 && (r.PreviousOwner != "" || r.Predecessor != "")) || (r.Epoch > 1 && (!ValidFingerprint(r.PreviousOwner) || !ValidFingerprint(r.Predecessor) || r.PreviousOwner == r.Owner)) {
		return errors.New("invalid shared ingress ownership predecessor")
	}
	if err := r.Policy.Validate(); err != nil {
		return err
	}
	if len(r.Policy.Origins) == 0 {
		return errors.New("shared ingress requires published origins")
	}
	digest, err := r.contentDigest()
	if err != nil {
		return err
	}
	if !ValidFingerprint(r.Digest) || r.Digest != digest {
		return errors.New("shared ingress digest mismatch")
	}
	return nil
}

// Pending persists both sides of a handoff. A prepared successor cannot serve
// as owner until the predecessor has durably committed the exact successor.
type Pending struct {
	Before    Record `json:"before"`
	Next      Record `json:"next"`
	Outgoing  bool   `json:"outgoing"`
	Committed bool   `json:"committed"`
}
type State struct {
	Revision uint64            `json:"revision,string"`
	Record   *Record           `json:"record,omitempty"`
	Pending  *Pending          `json:"pending,omitempty"`
	Receipts map[string]string `json:"activationReceipts,omitempty"`
}

func (s State) Clone() State {
	if s.Receipts != nil {
		receipts := make(map[string]string, len(s.Receipts))
		for caller, digest := range s.Receipts {
			receipts[caller] = digest
		}
		s.Receipts = receipts
	}
	if s.Record != nil {
		record := s.Record.Clone()
		s.Record = &record
	}
	if s.Pending != nil {
		p := *s.Pending
		p.Before = p.Before.Clone()
		p.Next = p.Next.Clone()
		s.Pending = &p
	}
	return s
}
func (s State) Validate(self string) error {
	if s.Revision > math.MaxInt64 {
		return errors.New("shared ingress storage revision exceeds its range")
	}
	if len(s.Receipts) > 64 {
		return errors.New("too many shared ingress activation receipts")
	}
	for caller, digest := range s.Receipts {
		if !ValidFingerprint(caller) || !ValidFingerprint(digest) {
			return errors.New("invalid shared ingress activation receipt")
		}
	}
	if s.Record == nil {
		if s.Pending != nil || len(s.Receipts) > 0 {
			return errors.New("ownership transfer has no current record")
		}
		return nil
	}
	if err := s.Record.Validate(); err != nil {
		return err
	}
	if !s.Record.Allows(self) {
		return ErrNotMember
	}
	if s.Pending == nil {
		return nil
	}
	p := s.Pending
	if err := p.Before.Validate(); err != nil {
		return err
	}
	if err := p.Next.Validate(); err != nil {
		return err
	}
	if !isSuccessor(p.Before, p.Next) || (p.Outgoing && p.Before.Owner != self) || (!p.Outgoing && p.Next.Owner != self) || (p.Committed && !p.Outgoing) {
		return errors.New("invalid prepared shared ingress transfer")
	}
	expected := p.Before.Digest
	if p.Committed {
		expected = p.Next.Digest
	}
	if s.Record.Digest != expected {
		return errors.New("prepared transfer does not match its durable record")
	}
	return nil
}
func isSuccessor(before, next Record) bool {
	if next.GroupID != before.GroupID || next.Version != before.Version+1 || next.Epoch != before.Epoch+1 || next.Owner == before.Owner || next.PreviousOwner != before.Owner || next.Predecessor != before.Digest {
		return false
	}
	expected := before.Clone()
	expected.Version, expected.Epoch, expected.Owner = next.Version, next.Epoch, next.Owner
	expected.PreviousOwner, expected.Predecessor = before.Owner, before.Digest
	expected, err := seal(expected)
	return err == nil && expected.Digest == next.Digest
}

type Store interface {
	Load() (State, error)
	CompareAndSwap(State, uint64) (State, error)
}

// Peer crosses the authenticated, explicitly authorized peer listener. Prepare
// and Activate must recheck trust after receiving a response. A failure after
// Commit is recovered by retrying this exact handoff, never by selecting a new
// routing computer or declaring an uncontactable owner dead.
type Peer interface {
	FetchShared(context.Context, string) (Record, error)
	PrepareShared(context.Context, string, Record, Record) (string, error)
	ActivateShared(context.Context, string, string) error
	AbortShared(context.Context, string, string) error
}
type Authority struct {
	operationMu sync.Mutex
	mu          sync.Mutex
	self        string
	store       Store
	state       State
	requests    map[uint64]context.CancelFunc
	sequence    uint64
}

func Open(self string, store Store) (*Authority, error) {
	if !ValidFingerprint(self) || store == nil {
		return nil, errors.New("shared ingress requires a stable identity and store")
	}
	state, err := store.Load()
	if err != nil {
		return nil, err
	}
	if err := state.Validate(self); err != nil {
		return nil, fmt.Errorf("restore shared ingress: %w", err)
	}
	return &Authority{self: self, store: store, state: state.Clone()}, nil
}
func (a *Authority) State() State { a.mu.Lock(); defer a.mu.Unlock(); return a.state.Clone() }
func (a *Authority) save(next State) error {
	if next.Receipts == nil && next.Record != nil && a.state.Record != nil && next.Record.GroupID == a.state.Record.GroupID {
		next.Receipts = a.state.Clone().Receipts
	}
	if err := next.Validate(a.self); err != nil {
		return err
	}
	saved, err := a.store.CompareAndSwap(next, a.state.Revision)
	if err != nil {
		return err
	}
	before, after := "", ""
	if a.state.Record != nil {
		before = a.state.Record.Digest
	}
	if saved.Record != nil {
		after = saved.Record.Digest
	}
	a.state = saved.Clone()
	if before != after {
		for _, cancel := range a.requests {
			cancel()
		}
	}
	return nil
}

type Role string

const (
	Gateway  Role = "gateway"
	Selector Role = "selector"
	Routing  Role = "routing"
)

// Begin binds a request to one committed record and authorized ingress role.
// A successful selector/membership/ownership change retires its requests and
// WebSockets. Staging, failed persistence, and cancellation of an uncommitted
// handoff leave the current serving record and its requests intact.
func (a *Authority) Begin(ctx context.Context, digest, actor string, role Role) (context.Context, Record, func(), error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.state.Record == nil {
		return nil, Record{}, nil, ErrUnavailable
	}
	record := a.state.Record
	if digest != record.Digest {
		return nil, Record{}, nil, ErrConflict
	}
	allowed := false
	switch role {
	case Gateway:
		allowed = actor == a.self && contains(record.Gateways, a.self)
	case Selector:
		allowed = a.self == record.Owner && contains(record.Gateways, actor)
	case Routing:
		allowed = a.self == record.RoutingOwner && actor == record.Owner
	}
	if !allowed {
		return nil, Record{}, nil, ErrNotMember
	}
	request, cancel := context.WithCancel(ctx)
	if a.requests == nil {
		a.requests = map[uint64]context.CancelFunc{}
	}
	a.sequence++
	id := a.sequence
	a.requests[id] = cancel
	return request, record.Clone(), func() { cancel(); a.mu.Lock(); delete(a.requests, id); a.mu.Unlock() }, nil
}
func (a *Authority) Create(policy access.Document, routingOwner string, gateways []string, expected uint64) (Record, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.state.Revision != expected || a.state.Record != nil {
		return Record{}, ErrConflict
	}
	var id [32]byte
	if _, err := rand.Read(id[:]); err != nil {
		return Record{}, err
	}
	gateways = append([]string(nil), gateways...)
	sort.Strings(gateways)
	record, err := seal(Record{SchemaVersion: SchemaVersion, GroupID: hex.EncodeToString(id[:]), Version: 1, Epoch: 1, Owner: a.self, RoutingOwner: routingOwner, Gateways: gateways, Policy: policy.Clone()})
	if err != nil {
		return Record{}, err
	}
	if err := a.save(State{Record: &record}); err != nil {
		return Record{}, err
	}
	return record.Clone(), nil
}
func (a *Authority) Update(policy access.Document, routingOwner string, gateways []string, expected uint64) (Record, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.state.Record == nil {
		return Record{}, ErrUnavailable
	}
	if a.state.Record.Owner != a.self {
		return Record{}, ErrNotOwner
	}
	if a.state.Pending != nil {
		return Record{}, ErrTransferPending
	}
	if a.state.Record.Version != expected {
		return Record{}, ErrConflict
	}
	next := a.state.Record.Clone()
	next.Version++
	next.Policy, next.RoutingOwner = policy.Clone(), routingOwner
	next.Gateways = append([]string(nil), gateways...)
	sort.Strings(next.Gateways)
	next, err := seal(next)
	if err != nil {
		return Record{}, err
	}
	if err := a.save(State{Record: &next}); err != nil {
		return Record{}, err
	}
	return next.Clone(), nil
}

// Join is a local-management action. Pairing alone never enrolls a gateway.
// The verified record must come directly from the declared current owner.
func (a *Authority) Join(ctx context.Context, owner string, expected uint64, peer Peer) (Record, error) {
	record, err := peer.FetchShared(ctx, owner)
	if err != nil {
		return Record{}, err
	}
	if err := record.Validate(); err != nil {
		return Record{}, err
	}
	if record.Owner != owner || !record.Allows(a.self) {
		return Record{}, ErrNotMember
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.state.Revision != expected || a.state.Record != nil {
		return Record{}, ErrConflict
	}
	if err := a.save(State{Record: &record}); err != nil {
		return Record{}, err
	}
	return record.Clone(), nil
}
func (a *Authority) Export(caller string) (Record, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.state.Record == nil {
		return Record{}, ErrUnavailable
	}
	if !a.state.Record.Allows(caller) {
		return Record{}, ErrNotMember
	}
	return a.state.Record.Clone(), nil
}

// Transfer stages a durable, immutable candidate before network work. It is
// safe to retry after timeouts or process restarts; recovery uses the same
// group/version/predecessor and destination, not a new ownership proposal.
func (a *Authority) Transfer(ctx context.Context, destination string, expected uint64, peer Peer) (Record, error) {
	a.operationMu.Lock()
	defer a.operationMu.Unlock()
	a.mu.Lock()
	if a.state.Record == nil {
		a.mu.Unlock()
		return Record{}, ErrUnavailable
	}
	if a.state.Pending == nil {
		current := a.state.Record.Clone()
		if current.Owner != a.self {
			a.mu.Unlock()
			return Record{}, ErrNotOwner
		}
		if current.Version != expected {
			a.mu.Unlock()
			return Record{}, ErrConflict
		}
		if destination == a.self || !current.Allows(destination) || !contains(current.Gateways, destination) {
			a.mu.Unlock()
			return Record{}, ErrNotMember
		}
		next := current.Clone()
		next.Owner, next.Version, next.Epoch = destination, current.Version+1, current.Epoch+1
		next.PreviousOwner, next.Predecessor = current.Owner, current.Digest
		next, err := seal(next)
		if err != nil {
			a.mu.Unlock()
			return Record{}, err
		}
		if err := a.save(State{Record: &current, Pending: &Pending{Before: current, Next: next, Outgoing: true}}); err != nil {
			a.mu.Unlock()
			return Record{}, err
		}
	}
	pending := *a.state.Pending
	if !pending.Outgoing || pending.Next.Owner != destination || pending.Before.Version != expected {
		a.mu.Unlock()
		return Record{}, ErrTransferPending
	}
	a.mu.Unlock()
	return a.finishTransfer(ctx, pending, peer)
}
func contains(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}
func (a *Authority) finishTransfer(ctx context.Context, pending Pending, peer Peer) (Record, error) {
	if !pending.Committed {
		ack, err := peer.PrepareShared(ctx, pending.Next.Owner, pending.Before, pending.Next)
		if err != nil {
			return Record{}, err
		}
		if ack != pending.Next.Digest {
			return Record{}, errors.New("successor prepared a different shared ingress record")
		}
		a.mu.Lock()
		if a.state.Pending == nil || a.state.Pending.Next.Digest != pending.Next.Digest {
			a.mu.Unlock()
			return Record{}, ErrConflict
		}
		if !a.state.Pending.Committed {
			p := *a.state.Pending
			p.Committed = true
			if err := a.save(State{Record: &p.Next, Pending: &p}); err != nil {
				a.mu.Unlock()
				return Record{}, err
			}
		}
		a.mu.Unlock()
	}
	if err := peer.ActivateShared(ctx, pending.Next.Owner, pending.Next.Digest); err != nil {
		return Record{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.state.Record == nil || a.state.Record.Digest != pending.Next.Digest {
		return Record{}, ErrConflict
	}
	if a.state.Pending != nil {
		if err := a.save(State{Record: a.state.Record}); err != nil {
			return Record{}, err
		}
	}
	return a.state.Record.Clone(), nil
}
func (a *Authority) Prepare(caller string, before, next Record) (string, error) {
	if err := before.Validate(); err != nil {
		return "", err
	}
	if err := next.Validate(); err != nil {
		return "", err
	}
	if caller != before.Owner || next.Owner != a.self || !isSuccessor(before, next) {
		return "", ErrNotOwner
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.state.Record == nil || a.state.Record.GroupID != before.GroupID || a.state.Record.Owner != caller {
		return "", ErrNotMember
	}
	if a.state.Pending != nil {
		if !a.state.Pending.Outgoing && a.state.Pending.Next.Digest == next.Digest {
			return next.Digest, nil
		}
		return "", ErrTransferPending
	}
	if before.Epoch != a.state.Record.Epoch || before.PreviousOwner != a.state.Record.PreviousOwner || before.Predecessor != a.state.Record.Predecessor || before.Version < a.state.Record.Version || (before.Version == a.state.Record.Version && before.Digest != a.state.Record.Digest) {
		return "", ErrConflict
	}
	if err := a.save(State{Record: &before, Pending: &Pending{Before: before, Next: next}}); err != nil {
		return "", err
	}
	return next.Digest, nil
}

// Activate consumes a record fetched from the authenticated predecessor, not
// its activation request alone. The peer listener independently reads that
// predecessor's durable record before calling this method.
func (a *Authority) Activate(caller string, committed Record) error {
	if err := committed.Validate(); err != nil {
		return err
	}
	digest := committed.Digest
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.state.Record == nil {
		return ErrUnavailable
	}
	if a.state.Pending == nil {
		if a.state.Receipts[caller] == digest {
			return nil
		}
		if a.state.Record.Owner == a.self && a.state.Record.Digest == digest && a.state.Record.PreviousOwner == caller {
			return nil
		}
		return ErrConflict
	}
	p := a.state.Pending
	if p.Outgoing || p.Before.Owner != caller || p.Next.Digest != digest {
		return ErrNotOwner
	}
	receipts := a.state.Clone().Receipts
	if receipts == nil {
		receipts = map[string]string{}
	}
	for previous := range receipts {
		if !contains(p.Next.Gateways, previous) {
			delete(receipts, previous)
		}
	}
	receipts[caller] = digest
	return a.save(State{Record: &p.Next, Receipts: receipts})
}

// Recover does not elect a new owner during a partition. The successor can
// activate only after reading the exact committed record from its predecessor.
func (a *Authority) Recover(ctx context.Context, peer Peer) (Record, error) {
	return a.recover(ctx, nil, peer)
}
func (a *Authority) RecoverAt(ctx context.Context, expected uint64, peer Peer) (Record, error) {
	return a.recover(ctx, &expected, peer)
}
func (a *Authority) recover(ctx context.Context, expected *uint64, peer Peer) (Record, error) {
	a.operationMu.Lock()
	defer a.operationMu.Unlock()
	a.mu.Lock()
	if expected != nil && a.state.Revision != *expected {
		a.mu.Unlock()
		return Record{}, ErrConflict
	}
	if a.state.Pending == nil {
		a.mu.Unlock()
		return Record{}, ErrTransferPending
	}
	p := *a.state.Pending
	a.mu.Unlock()
	if p.Outgoing {
		return a.finishTransfer(ctx, p, peer)
	}
	committed, err := peer.FetchShared(ctx, p.Before.Owner)
	if err != nil {
		return Record{}, err
	}
	if err := committed.Validate(); err != nil {
		return Record{}, err
	}
	if committed.Digest != p.Next.Digest {
		return Record{}, errors.New("predecessor has not committed this ownership transfer")
	}
	if err := a.Activate(p.Before.Owner, committed); err != nil {
		return Record{}, err
	}
	return committed.Clone(), nil
}

func (a *Authority) ActivationKnown(caller, digest string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.state.Pending == nil && a.state.Receipts[caller] == digest
}
func (a *Authority) Abort(caller, digest string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.state.Pending == nil {
		return nil
	}
	p := a.state.Pending
	if p.Outgoing || p.Before.Owner != caller || p.Next.Digest != digest {
		return ErrNotOwner
	}
	return a.save(State{Record: &p.Before})
}
func (a *Authority) Cancel(ctx context.Context, peer Peer) error {
	return a.cancel(ctx, nil, peer)
}
func (a *Authority) CancelAt(ctx context.Context, expected uint64, peer Peer) error {
	return a.cancel(ctx, &expected, peer)
}
func (a *Authority) cancel(ctx context.Context, expected *uint64, peer Peer) error {
	a.operationMu.Lock()
	defer a.operationMu.Unlock()
	a.mu.Lock()
	if expected != nil && a.state.Revision != *expected {
		a.mu.Unlock()
		return ErrConflict
	}
	if a.state.Pending == nil {
		a.mu.Unlock()
		return nil
	}
	p := *a.state.Pending
	if !p.Outgoing || p.Committed {
		a.mu.Unlock()
		return ErrTransferPending
	}
	a.mu.Unlock()
	if err := peer.AbortShared(ctx, p.Next.Owner, p.Next.Digest); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.state.Pending == nil {
		return nil
	}
	if a.state.Pending.Next.Digest != p.Next.Digest || a.state.Pending.Committed {
		return ErrConflict
	}
	return a.save(State{Record: &p.Before})
}

// Sync accepts changes only from the cached current authority. Equal versions
// must have identical content; a transfer must be the next ownership epoch and
// must name the authenticated predecessor. A missing handoff chain fails closed.
func (a *Authority) Sync(ctx context.Context, peer Peer) (Record, error) {
	a.operationMu.Lock()
	defer a.operationMu.Unlock()
	a.mu.Lock()
	if a.state.Record == nil {
		a.mu.Unlock()
		return Record{}, ErrUnavailable
	}
	if a.state.Pending != nil {
		a.mu.Unlock()
		return Record{}, ErrTransferPending
	}
	current := a.state.Record.Clone()
	if current.Owner == a.self {
		a.mu.Unlock()
		return current, nil
	}
	a.mu.Unlock()
	next, err := peer.FetchShared(ctx, current.Owner)
	if err != nil {
		return Record{}, err
	}
	if err := next.Validate(); err != nil {
		return Record{}, err
	}
	if next.GroupID != current.GroupID || next.Version < current.Version || (next.Version == current.Version && next.Digest != current.Digest) {
		return Record{}, ErrConflict
	}
	if next.Owner == a.self && current.Owner != a.self {
		return Record{}, ErrTransferPending
	}
	if next.Owner == current.Owner {
		if next.Epoch != current.Epoch || next.PreviousOwner != current.PreviousOwner || next.Predecessor != current.Predecessor {
			return Record{}, ErrConflict
		}
	} else if next.PreviousOwner != current.Owner || next.Epoch != current.Epoch+1 || next.Version <= current.Version {
		return Record{}, ErrNotOwner
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.state.Record == nil || a.state.Record.Digest != current.Digest || a.state.Pending != nil {
		return Record{}, ErrConflict
	}
	if next.Digest == current.Digest {
		return current, nil
	}
	if err := a.save(State{Record: &next}); err != nil {
		return Record{}, err
	}
	return next.Clone(), nil
}
