# Rementor Routing

Rementor describes stable development URLs and chooses the services behind them
across one person's devices and remote environments.

## Language

**Workspace**:
A named development environment containing application bindings and routing intent.
_Avoid_: Machine, team

**Application identity**:
The stable identity of an application or service across its workspace bindings.
_Avoid_: Hostname, route

**Routing session**:
An isolated feature environment under a workspace, with a pinned remote baseline
and its own endpoint registrations and route choices.
_Avoid_: Rementor instance, live inherited environment

**Public origin**:
The scheme, hostname, and port visible to a client, independent of the device
serving it. One workspace may have several unrelated public origins.
_Avoid_: Upstream, machine URL

**Route**:
A public origin and path matcher associated with an application identity and a
selected target. Changing the target preserves the public URL.
_Avoid_: Application, local port

**Target**:
A destination for a route: a remote upstream, an endpoint owned by a device, or
another logical route's selection.
_Avoid_: Local versus remote boolean

**Node**:
An enrolled device capable of hosting Rementor or application endpoints.
_Avoid_: Coordinator, client

**Client device**:
The device making a routed request, whether or not Rementor is installed on it.
_Avoid_: User identity, target node

**Shared default**:
The routing selections inherited by client devices without an override.
_Avoid_: Global local mode

**Rementor instance**:
A member of the user's fleet that owns routing intent and can serve requests.
It may follow another instance’s complete environment configuration. Edits to
followed routes change the source instance. Extra environments remain local.
An environment can stop following, keeping its current setup for local edits.
_Avoid_: Client device, routing profile

**Followed configuration**:
The environments and their routing sessions followed from another instance.
Source edits propagate to followers; independent local environments and sessions
keep their own configuration and pinned baselines, including when an environment
stops following.
_Avoid_: Session attachment, per-service receiver override

**Inherited environment**:
An environment in a followed configuration. An inherited endpoint keeps
its owning device; inheriting Brutus's local service still targets Brutus.
_Avoid_: Configuration copy, local port on the inheriting device

**Ingress**:
The entry point that receives a request for a public origin and applies the
serving instance's effective routing selections.
_Avoid_: Shared-state authority

**Shared-state authority**:
The role that orders changes to shared routing intent; it is independent of the
node receiving a client request.
_Avoid_: Permanent coordinator, ingress
