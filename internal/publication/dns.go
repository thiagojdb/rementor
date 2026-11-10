package publication

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strings"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// DNS is authoritative only for the configured publication zone. No query is
// forwarded, and arbitrary wildcard names never expose an unpublished scope.
func (m *Manager) answerDNS(query []byte) []byte {
	var msg dnsmessage.Message
	if err := msg.Unpack(query); err != nil || msg.Header.Response {
		return nil
	}
	response := dnsmessage.Message{Header: dnsmessage.Header{ID: msg.ID, Response: true, OpCode: msg.OpCode}}
	if len(msg.Questions) != 1 || msg.Header.OpCode != 0 {
		response.Header.RCode = dnsmessage.RCodeFormatError
		raw, _ := response.Pack()
		return raw
	}
	q := msg.Questions[0]
	response.Questions = msg.Questions
	m.mu.RLock()
	c := m.state.Configuration
	address := m.state.ListenAddress
	if address == "" {
		address = c.ListenAddress
	}
	known := false
	name := strings.ToLower(strings.TrimSuffix(q.Name.String(), "."))
	for _, s := range m.state.Scopes {
		for _, l := range s.Links {
			if strings.TrimPrefix(l.URL, "https://") == name {
				known = true
			}
		}
	}
	m.mu.RUnlock()
	if !c.Enabled || q.Class != dnsmessage.ClassINET || !(name == c.Suffix || strings.HasSuffix(name, "."+c.Suffix)) {
		response.Header.RCode = dnsmessage.RCodeRefused
	} else {
		response.Header.Authoritative = true
		if !known {
			response.Header.RCode = dnsmessage.RCodeNameError
		} else {
			header := dnsmessage.ResourceHeader{Name: q.Name, Class: dnsmessage.ClassINET, TTL: 10}
			ip := net.ParseIP(address)
			if q.Type == dnsmessage.TypeA && ip.To4() != nil {
				var a [4]byte
				copy(a[:], ip.To4())
				header.Type = dnsmessage.TypeA
				response.Answers = []dnsmessage.Resource{{Header: header, Body: &dnsmessage.AResource{A: a}}}
			}
			if q.Type == dnsmessage.TypeAAAA && ip != nil && ip.To4() == nil {
				var a [16]byte
				copy(a[:], ip.To16())
				header.Type = dnsmessage.TypeAAAA
				response.Answers = []dnsmessage.Resource{{Header: header, Body: &dnsmessage.AAAAResource{AAAA: a}}}
			}
		}
	}
	raw, _ := response.Pack()
	return raw
}
func (m *Manager) serveDNSUDP(conn net.PacketConn) {
	buf := make([]byte, 4096)
	for {
		n, addr, err := conn.ReadFrom(buf)
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				m.mu.Lock()
				m.state.DNSActive = false
				m.state.DNSError = err.Error()
				m.mu.Unlock()
			}
			return
		}
		host, _, err := net.SplitHostPort(addr.String())
		if err != nil || !TailnetIP(net.ParseIP(host)) {
			continue
		}
		if response := m.answerDNS(buf[:n]); len(response) > 0 {
			_, _ = conn.WriteTo(response, addr)
		}
	}
}
func (m *Manager) serveDNSTCP(listener net.Listener) {
	slots := make(chan struct{}, 64)
	for {
		conn, err := listener.Accept()
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				m.mu.Lock()
				m.state.DNSActive = false
				m.state.DNSError = err.Error()
				m.mu.Unlock()
			}
			return
		}
		host, _, err := net.SplitHostPort(conn.RemoteAddr().String())
		if err != nil || !TailnetIP(net.ParseIP(host)) {
			conn.Close()
			continue
		}
		select {
		case slots <- struct{}{}:
		default:
			conn.Close()
			continue
		}
		go func() {
			defer conn.Close()
			defer func() { <-slots }()
			for i := 0; i < 32; i++ {
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				var size [2]byte
				if _, err := io.ReadFull(conn, size[:]); err != nil {
					return
				}
				n := int(binary.BigEndian.Uint16(size[:]))
				if n > 4096 || n < 12 {
					return
				}
				query := make([]byte, n)
				if _, err := io.ReadFull(conn, query); err != nil {
					return
				}
				response := m.answerDNS(query)
				if len(response) == 0 {
					return
				}
				binary.BigEndian.PutUint16(size[:], uint16(len(response)))
				if _, err := conn.Write(append(size[:], response...)); err != nil {
					return
				}
			}
		}()
	}
}
