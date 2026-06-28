package store

type HashFunc func(string) (string, error)

// BootstrapAdmin creates the first admin (must_change_password=1) if none exist.
// It is a no-op when an admin already exists — the contract is "ensure at most
// one bootstrap admin", NOT "create or fail". This means empty bootstrap creds
// on an admin-bearing DB (the normal restart case, where no BOOTSTRAP_* env is
// set) MUST NOT error: the count check below runs before credential validation.
//
// When no admin exists AND no creds are provided, the function also returns
// nil (defense-in-depth): the server still boots, and an admin can later be
// created via the admin API once a bootstrap admin exists. We never fatal-exit
// the server merely because of missing bootstrap env vars.
func BootstrapAdmin(db *DB, username, password string, hash HashFunc) error {
	var exists int
	if err := db.sql.QueryRow("SELECT count(*) FROM users WHERE role = 'admin'").Scan(&exists); err != nil {
		return err
	}
	if exists > 0 {
		return nil
	}
	if username == "" || password == "" {
		// No admin exists and no bootstrap creds provided: skip rather than
		// fatal. The server still starts; an admin can be created later via
		// the admin API once a bootstrap admin exists. Returning nil keeps
		// the server bootable in an admin-free state.
		return nil
	}
	hashed, err := hash(password)
	if err != nil {
		return err
	}
	uid, err := db.AllocateUID()
	if err != nil {
		return err
	}
	_, err = db.CreateUser(User{
		UID: uid, Username: username, PasswordHash: hashed,
		Role: "admin", MustChangePassword: true,
	})
	return err
}