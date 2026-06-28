package store

import "errors"

type HashFunc func(string) (string, error)

// BootstrapAdmin creates the first admin (must_change_password=1) if none exist.
// No-op when an admin already exists.
func BootstrapAdmin(db *DB, username, password string, hash HashFunc) error {
	if username == "" || password == "" {
		return errors.New("bootstrap admin requires username and password")
	}
	var exists int
	if err := db.sql.QueryRow("SELECT count(*) FROM users WHERE role = 'admin'").Scan(&exists); err != nil {
		return err
	}
	if exists > 0 {
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
