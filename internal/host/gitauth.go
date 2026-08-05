package host

import "encoding/base64"

// GitAuthArgs returns the git global flags that authenticate ONE git
// invocation as credential, or nil when credential is empty.
//
// Every git subprocess in this product that talks to a remote goes through
// these args — push, ls-remote, the recovery fetch, and (since the private-
// space repair) internal/space's mirror clone/fetch. It is exported because
// internal/space needs it: the mirror read path was the one network git call
// that carried no credential at all, which made a space unreadable the moment
// its repository stopped being public.
//
// Two properties this shape exists for, both of which are about the token
// never outliving the process:
//
//  1. `-c http.extraheader=...` is a per-invocation config override. It is
//     never written to any file and never becomes part of a remote URL that
//     git would echo back in an error message, print in `remote -v`, or store
//     in `remote.origin.url`.
//  2. The returned args MUST be placed BEFORE the git subcommand
//     (`git -c ... clone`, never `git clone -c ...`). Git's own rule is that
//     `-c` after `clone` is persisted into the NEW repository's config — which
//     on this product's machine-shared mirrors would leave the token sitting
//     in `<mirror>/.git/config` for every project on the machine to read.
//     Callers are responsible for the ordering; TestGitAuthArgs pins the
//     contract, and internal/space routes every network git call through one
//     function so there is a single place that can get it wrong.
//
// The empty credential returning nil is load-bearing, not a convenience: a
// public space must stay readable with no token at all, so "no credential"
// has to mean "run git tokenless", never "fail".
func GitAuthArgs(credential Credential) []string {
	if credential.Token == "" {
		return nil
	}
	basicAuth := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + credential.Token))
	return []string{"-c", "http.extraheader=AUTHORIZATION: basic " + basicAuth}
}
