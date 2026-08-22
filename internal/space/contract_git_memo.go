package space

import (
	"context"
	"strconv"
	"strings"
	"sync"
)

// A DASHBOARD RENDER RE-RUNS THE SAME GIT COMMAND FOUR TIMES OUT OF FIVE.
//
// Measured in a real space on 2026-08-22: `a2a html` spent 14.0 s of a 14.2 s
// render inside contract-version history, and a shim on PATH counted 2 197
// `git` invocations against 481 DISTINCT commands — 906 `cat-file` for 145
// objects, 372 `ls-tree` for 108 trees, 302 `rev-list` for 77 commits, 292
// `diff-tree` for 73. A process spawn costs ~6 ms here, so the forks WERE the
// render. `resolveContractVersionDetails` re-walks the same history once per
// consumer pin, which is a caller shape this package cannot see.
//
// So the seam remembers instead. This is a memo, not a cache with a lifetime
// policy: it lives exactly as long as the context that installs it, which is
// one render, and no caller that does not ask for it is affected at all.
//
// WHAT IT WILL NOT REMEMBER, and why the list is an allowlist rather than a
// denylist: only object-addressed reads. A command naming a REF can answer
// differently while the render runs — a fetch landing under it, another
// process moving a branch — and a memo keyed on argv would serve the stale
// answer with no way to notice. Every `cat-file`, `ls-tree`, `diff-tree` and
// `rev-list` this package issues names a resolved 40-hex object (verified
// against the same 2 197-call trace: zero of them mention HEAD, refs/ or a
// branch). `rev-parse` is exactly the ref-resolving one, and `git log` with no
// revision means HEAD — neither is memoized, at a measured cost of ~236 forks
// left on the table.
type contractGitMemo struct {
	mu        sync.Mutex
	entries   map[string][]byte
	remaining int64
}

// contractGitMemoBudget bounds what one render may hold. Each entry is already
// bounded by its own read limit; this bounds the sum, so a space with a large
// contract corpus cannot turn a render into a memory incident.
const contractGitMemoBudget = 64 << 20

type contractGitMemoKey struct{}

// WithContractGitCache returns a context whose bounded contract-git reads are
// memoized for the life of that context. Install it around ONE read-only
// operation — a dashboard render, a report — never around a long-lived process
// or anything that writes to the repository it reads.
//
// It is opt-in on purpose. Verification paths that must observe the repository
// as it is now simply do not call this, and they behave exactly as they did
// before this file existed.
func WithContractGitCache(ctx context.Context) context.Context {
	if ctx == nil {
		return nil
	}
	if _, ok := ctx.Value(contractGitMemoKey{}).(*contractGitMemo); ok {
		return ctx // already installed; nesting must not split the memo in two
	}
	return context.WithValue(ctx, contractGitMemoKey{}, &contractGitMemo{
		entries:   map[string][]byte{},
		remaining: contractGitMemoBudget,
	})
}

func contractGitMemoFrom(ctx context.Context) *contractGitMemo {
	if ctx == nil {
		return nil
	}
	memo, _ := ctx.Value(contractGitMemoKey{}).(*contractGitMemo)
	return memo
}

// contractGitMemoizable reports whether this exact invocation is object
// addressed, and therefore answers the same way for as long as the objects
// exist. See the type comment for why this is an allowlist.
func contractGitMemoizable(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "cat-file", "ls-tree", "diff-tree", "rev-list":
	case "rev-parse":
		// `rev-parse` is the ref-resolving verb, so it is admitted only in the
		// one shape that cannot move: a fully resolved object id, optionally
		// with a peeling suffix (`<oid>^{commit}`). That is what
		// contractGitResolveCommit issues for an already-resolved commit — 312
		// calls for 78 distinct objects in the measured render. A branch name
		// or HEAD reaching here is refused by the same rule, without anyone
		// having to remember which callers pass what.
		if len(args) < 2 || !startsWithObjectID(args[len(args)-1]) {
			return false
		}
	default:
		return false
	}
	for _, arg := range args[1:] {
		if arg == "HEAD" || arg == "@" ||
			strings.HasPrefix(arg, "refs/") ||
			strings.HasPrefix(arg, "HEAD~") ||
			strings.HasPrefix(arg, "HEAD^") {
			return false
		}
	}
	return true
}

// startsWithObjectID reports whether value begins with a full 40-hex object
// id. A suffix (`^{commit}`, `:path`) is allowed: both are pure functions of
// an immutable object, so the answer cannot change while the render runs.
func startsWithObjectID(value string) bool {
	if len(value) < 40 {
		return false
	}
	for i := 0; i < 40; i++ {
		c := value[i]
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
		case c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}

// contractGitMemoKeyFor includes the read LIMIT, not only the argv. The same
// command under a smaller ceiling is a different question — it can legitimately
// answer with a bounded-result refusal — and a key that dropped the limit would
// hand one caller's ceiling to another.
func contractGitMemoKeyFor(repoDir string, limit int64, args []string) string {
	var b strings.Builder
	b.WriteString(repoDir)
	b.WriteByte(0)
	b.WriteString(strings.Join(args, "\x00"))
	b.WriteByte(0)
	b.WriteString(strconv.FormatInt(limit, 10))
	return b.String()
}

func (m *contractGitMemo) get(key string) ([]byte, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	raw, ok := m.entries[key]
	if !ok {
		return nil, false
	}
	return append([]byte(nil), raw...), true
}

// put stores a SUCCESSFUL read only. An error is never memoized: a bounded-read
// refusal and a transient failure are spelled the same way to a caller, and
// remembering one would turn a passing condition into a permanent verdict for
// the rest of the render.
func (m *contractGitMemo) put(key string, raw []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	size := int64(len(raw))
	if size > m.remaining {
		return // over budget: still serve what is already held, remember no more
	}
	if _, exists := m.entries[key]; exists {
		return
	}
	m.remaining -= size
	m.entries[key] = append([]byte(nil), raw...)
}
