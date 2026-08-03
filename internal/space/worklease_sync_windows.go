//go:build windows

package space

// syncDirectory is intentionally a no-op on Windows. The lease bytes are
// synced before their atomic rename. Go exposes no safe rooted directory
// metadata fsync on Windows, and these leases are disposable advisory state;
// a power-loss window therefore yields unknown state instead of a false error
// after a successful rename.
func (*WorkLeaseStore) syncDirectory() error {
	return nil
}
