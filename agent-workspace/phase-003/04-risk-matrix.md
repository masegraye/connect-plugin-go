# Risk Matrix

## Risk Rating Criteria

### Severity Levels

| Level | Definition | Example |
|-------|------------|---------|
| **Critical** | Immediate security breach possible, no user interaction required | RCE, auth bypass, data exposure |
| **High** | Significant security impact, may require user interaction | Privilege escalation, token theft |
| **Medium** | Security impact under specific conditions | DoS, information disclosure |
| **Low** | Minor security impact or theoretical | Timing attacks, log exposure |

### Likelihood Levels

| Level | Definition |
|-------|------------|
| **High** | Easy to exploit, common attack vector |
| **Medium** | Requires specific conditions or knowledge |
| **Low** | Requires significant effort or rare conditions |

### Risk Score

```
Risk = Severity × Likelihood

High severity × High likelihood = Critical Risk
High severity × Medium likelihood = High Risk
High severity × Low likelihood = Medium Risk
Medium severity × High likelihood = High Risk
Medium severity × Medium likelihood = Medium Risk
Medium severity × Low likelihood = Low Risk
Low severity × Any likelihood = Low Risk
```

## Detailed Risk Register

### Authentication Risks

| ID | Risk | Severity | Likelihood | Risk Score | Affected Component |
|----|------|----------|------------|------------|-------------------|
| AUTH-001 | mTLS interceptor bypasses actual certificate validation | High | High | **Critical** | `auth_mtls.go:57-84` |
| AUTH-002 | Token comparison vulnerable to timing attacks | Medium | Low | Low | `handshake.go:189` |
| AUTH-003 | Magic cookie provides false sense of security | Medium | Medium | Medium | `handshake.go:17-22` |
| AUTH-004 | Runtime tokens never expire | Medium | Medium | Medium | `handshake.go:91-93` |
| AUTH-005 | Grant tokens never expire | Medium | Medium | Medium | `broker.go:99-106` |

### Authorization Risks

| ID | Risk | Severity | Likelihood | Risk Score | Affected Component |
|----|------|----------|------------|------------|-------------------|
| AUTHZ-001 | Any plugin can register any service type | Medium | Medium | Medium | `registry.go:96-136` |
| AUTHZ-002 | No access control on service discovery | Low | Low | Low | `registry.go:367-395` |
| AUTHZ-003 | Any plugin can request any capability | Medium | Low | Low | `broker.go:77-117` |

### Input Validation Risks

| ID | Risk | Severity | Likelihood | Risk Score | Affected Component |
|----|------|----------|------------|------------|-------------------|
| INPUT-001 | Metadata fields unbounded | Medium | Medium | Medium | `registry.go:121` |
| INPUT-002 | Service type names not validated | Low | Low | Low | `registry.go:117` |
| INPUT-003 | Self-ID not fully sanitized | Low | Low | Low | `handshake.go:196-202` |

### Denial of Service Risks

| ID | Risk | Severity | Likelihood | Risk Score | Affected Component |
|----|------|----------|------------|------------|-------------------|
| DOS-001 | Registration flooding | Medium | Medium | Medium | `registry.go:96-136` |
| DOS-002 | Watcher flooding | Low | Low | Low | `registry.go:406-456` |
| DOS-003 | Handshake flooding | Medium | Medium | Medium | `handshake.go:43-171` |

### Information Disclosure Risks

| ID | Risk | Severity | Likelihood | Risk Score | Affected Component |
|----|------|----------|------------|------------|-------------------|
| INFO-001 | Router logs sensitive metadata | Low | High | Low | `router.go:116-131` |
| INFO-002 | Error messages reveal internal state | Low | Medium | Low | Multiple files |

### Process/Launch Risks

| ID | Risk | Severity | Likelihood | Risk Score | Affected Component |
|----|------|----------|------------|------------|-------------------|
| PROC-001 | Environment variable injection | Low | Low | Low | `launch_process.go:46-49` |
| PROC-002 | Binary path not validated | Medium | Low | Low | `launch_process.go:45` |
| PROC-003 | In-memory plugins share process space | Medium | Low | Low | `launch_inmemory.go` |

### Cryptographic Risks

| ID | Risk | Severity | Likelihood | Risk Score | Affected Component |
|----|------|----------|------------|------------|-------------------|
| CRYPTO-001 | rand.Read failure causes panic | Low | Very Low | Low | `handshake.go:208-213` |
| CRYPTO-002 | No TLS enforcement warning | Medium | Medium | Medium | `client.go:147` |

### Protocol Risks

| ID | Risk | Severity | Likelihood | Risk Score | Affected Component |
|----|------|----------|------------|------------|-------------------|
| PROTO-001 | No replay protection | Medium | Medium | Medium | Handshake protocol |
| PROTO-002 | Protocol downgrade possible | Low | Low | Low | Version negotiation |
| PROTO-003 | Man-in-the-middle without TLS | High | Medium | High | All network communication |

## Risk Heat Map

```
                    LIKELIHOOD
                Low      Medium     High
           ┌─────────┬──────────┬──────────┐
     High  │ PROTO-  │          │ AUTH-001 │
           │ 003     │          │          │
SEVERITY   ├─────────┼──────────┼──────────┤
    Medium │ AUTHZ-  │ AUTH-003 │          │
           │ 001,003 │ AUTH-004 │          │
           │ PROC-02 │ AUTH-005 │          │
           │ PROC-03 │ DOS-001  │          │
           │         │ DOS-003  │          │
           │         │ PROTO-01 │          │
           │         │ CRYPTO-2 │          │
           │         │ INPUT-01 │          │
           ├─────────┼──────────┼──────────┤
     Low   │ AUTH-02 │ INFO-002 │ INFO-001 │
           │ AUTHZ-2 │          │          │
           │ INPUT-2 │          │          │
           │ INPUT-3 │          │          │
           │ DOS-002 │          │          │
           │ PROC-01 │          │          │
           │ CRYPTO-1│          │          │
           │ PROTO-2 │          │          │
           └─────────┴──────────┴──────────┘
```

## Risk Treatment Plan

### Accept

These risks are acceptable given the design constraints:

- **AUTH-002** (Timing attack): Low exploitability, standard token length
- **INFO-001** (Router logs): Operational necessity, add log level control
- **PROC-003** (In-memory isolation): Documented trade-off for testing

### Mitigate Before Release

These risks require code changes before open source release:

| Risk | Mitigation | Effort |
|------|------------|--------|
| AUTH-001 | Implement proper mTLS certificate extraction | Medium |
| PROTO-003 | Add TLS enforcement warning, update documentation | Low |
| AUTH-003 | Document magic cookie limitations, add warning | Low |
| CRYPTO-001 | Return error instead of panic | Low |

### Mitigate Before Production

These risks should be addressed before production use:

| Risk | Mitigation | Effort |
|------|------------|--------|
| DOS-001 | Add rate limiting middleware | Medium |
| AUTH-004, AUTH-005 | Add token expiration | Medium |
| AUTHZ-001 | Add service registration authorization | Medium |
| INPUT-001 | Add metadata validation | Low |
| PROTO-001 | Add request nonce/timestamp | Medium |

### Monitor

These risks should be monitored but don't require immediate action:

- **DOS-002**: Watcher flooding (low impact)
- **INFO-002**: Error messages (minor information disclosure)
- **PROC-001**: Env variable injection (controlled deployment)

## Risk Ownership

| Category | Owner Recommendation |
|----------|---------------------|
| Authentication | Library maintainer |
| Authorization | Library maintainer (default), User (custom policies) |
| Input Validation | Library maintainer |
| DoS | User (infrastructure level), Library (sane defaults) |
| TLS/Encryption | User (deployment), Library (documentation) |
| Process Security | User (deployment model choice) |

## Residual Risk Assessment

After implementing recommended mitigations:

| Category | Current Risk | Residual Risk |
|----------|--------------|---------------|
| Authentication | Medium-High | Low |
| Authorization | Medium | Low-Medium |
| Input Validation | Medium | Low |
| DoS | Medium | Low |
| Information Disclosure | Low | Low |
| Process Security | Low | Low |
| Protocol Security | Medium | Low |

**Overall Residual Risk: LOW** (after mitigations)

## Comparison with Similar Systems

| Risk Category | connect-plugin-go | go-plugin | gRPC (raw) |
|---------------|-------------------|-----------|------------|
| Process Isolation | Medium | Low | N/A |
| Network Security | Medium | Low | Medium |
| Auth Complexity | Low | Very Low | High |
| DoS Surface | Medium | Low | High |
| Overall | Medium | Low | Medium-High |

**Note:** go-plugin has lower risk primarily because it uses Unix sockets (no network exposure) and always spawns subprocesses (process isolation). connect-plugin-go accepts higher risk for network flexibility.
