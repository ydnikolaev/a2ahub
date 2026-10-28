package host

// gitOps is the git mechanics every host that stores its space in a git
// repository shares: pushing a head branch, reading one remote branch, and
// the recovery readers the write funnel uses to adopt an orphaned branch.
// None of them reads host state (there is no field to read: the credential
// and the remote arrive in each request), so both GitHubHost and LocalHost
// embed this one type instead of carrying a copy. GitHub behaviour is
// byte-identical to the methods' former home on *GitHubHost; a host with no
// credential passes the zero Credential and GitAuthArgs yields no flags.
//
// The methods are declared beside the concern they serve: PushBranch and
// ReadRemoteBranch in github.go (they spawn git directly, and the lint
// allowlist for that is an exact file path, so they stay where it already
// names them), the recovery readers in remote_recovery.go, remote_heads.go
// and remote_tree.go.
type gitOps struct{}
