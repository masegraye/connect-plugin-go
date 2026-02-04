# PR #4: Pre-Merge Documentation Checklist

**Quick Reference:** Documentation-only changes needed before merge
**Estimated Effort:** 2-4 hours
**All code changes approved** ✅ - Only documentation enhancements required

---

## Documentation Tasks

### 1. Create `docs/performance.md` (New File)

**Content to include:**
- [ ] Token validation lock contention section
  - Current behavior explanation
  - Monitoring guidance
  - Short-term mitigation strategies
  - Phase 3 optimization roadmap
- [ ] Pre-production benchmarking guide
  - Token validation throughput test
  - Rate limiter throughput test
  - End-to-end latency test
- [ ] Load testing scenarios
  - Steady state (1000 req/s)
  - Burst traffic (5000 req/s spike)
  - Rate limit enforcement verification
- [ ] Performance regression detection
  - CI integration example
  - Metrics to monitor
  - Scaling guidance

**Template provided in:** `PR-documentation-gaps.md` Section 2

---

### 2. Create/Enhance `docs/testing.md`

**Content to include:**
- [ ] Manual verification section for capability grant expiration
  - Step-by-step curl commands
  - Expected behavior documentation
- [ ] Security test suite overview
  - List of test categories
  - How to run security tests
  - Coverage summary

**Template provided in:** `PR-documentation-gaps.md` Section 6

---

### 3. Enhance `docs/guides/rate-limiting.md`

**New section to add:** "Distributed Deployments"

- [ ] Multi-replica limitation warning
  - Clear explanation with example (100 req/s × 3 replicas = 300 req/s)
- [ ] Replica count adjustment formula with code example
- [ ] External rate limiting alternatives
  - Kong, Envoy, nginx options
- [ ] Phase 3 roadmap reference

**Template provided in:** `PR-documentation-gaps.md` Section 1

---

### 4. Enhance `docs/security.md`

**New sections to add:**

**A. Token Replay Protection**
- [ ] Current behavior explanation
- [ ] Threat scenario walkthrough
- [ ] Mitigation strategies
  - TLS requirement (with code)
  - TTL reduction guidance
  - Token rotation pattern (application-level)
- [ ] Phase 3 enhancements preview

**B. TLS Enforcement Checklist**
- [ ] Pre-deployment verification checklist (5 items)
- [ ] Common misconfigurations section
  - Mixed TLS/non-TLS endpoints
  - Self-signed cert validation disabled
- [ ] Correct configuration examples

**Template provided in:** `PR-documentation-gaps.md` Sections 3 & 4

---

### 5. Enhance `README.md`

**Add at top (after title, before quick start):**

- [ ] Prominent security notice box
  - TLS required for production
  - Risk explanation (credential theft, MITM)
- [ ] Side-by-side code examples
  - ❌ Development (insecure)
  - ✅ Production (TLS)
- [ ] Link to full security guide

**Template provided in:** `PR-documentation-gaps.md` Section 4.A

---

## Verification Checklist

After completing documentation tasks:

- [ ] All new sections use consistent formatting
- [ ] Code examples are syntactically correct
- [ ] All internal links work (`[text](../path.md)`)
- [ ] External links are valid (GitHub issues, etc.)
- [ ] No TODO comments left in documentation
- [ ] Markdown renders correctly (preview in GitHub)
- [ ] Security warnings are prominent and clear
- [ ] Technical accuracy reviewed

---

## Quick Start: Documentation Writing

### 1. Create New Files

```bash
touch docs/performance.md
touch docs/testing.md  # If doesn't exist
```

### 2. Copy Template Content

Open `PR-documentation-gaps.md` and copy the markdown sections under each heading into the corresponding files.

### 3. Customize for Context

- Replace placeholder issue numbers (e.g., `#XXX`) with actual GitHub issue links
- Verify all code examples compile/run
- Adjust paths if directory structure differs

### 4. Verify Links

```bash
# Check for broken internal links
find docs -name "*.md" -exec grep -H '\[.*\](.*\.md)' {} \;

# Manually verify each link resolves correctly
```

---

## Approval Criteria

PR #4 is **ready to merge** when:

✅ All code changes complete (already done)
✅ All tests passing (already done)
✅ Documentation tasks above completed
✅ Links verified
✅ Technical accuracy reviewed

**No code changes required** - This is documentation-only work.

---

## Time Estimates

| Task | Estimated Time |
|------|----------------|
| Create `docs/performance.md` | 45 min |
| Create/enhance `docs/testing.md` | 20 min |
| Enhance `docs/guides/rate-limiting.md` | 30 min |
| Enhance `docs/security.md` | 45 min |
| Enhance `README.md` | 15 min |
| Verification & links | 30 min |
| **Total** | **3 hours** |

---

## Post-Merge: Phase 3 Tracking

Create GitHub issues for deferred improvements:

1. **Token validation optimization** (#TBD)
   - Implement read-lock fast path
   - Add benchmark comparison

2. **Distributed rate limiting** (#TBD)
   - Design pluggable interface
   - Implement Redis backend

3. **Performance benchmarks in CI** (#TBD)
   - Add benchmark suite
   - Enable regression detection

4. **Enhanced observability** (#TBD)
   - Rate limiter metrics
   - Token validation latency tracking

Label all as: `enhancement`, `phase-3`, `security`
