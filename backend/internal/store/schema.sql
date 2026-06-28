CREATE TABLE IF NOT EXISTS users (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  uid INTEGER UNIQUE NOT NULL,
  username TEXT UNIQUE NOT NULL,
  password_hash TEXT NOT NULL,
  role TEXT NOT NULL CHECK(role IN ('admin','user')),
  must_change_password INTEGER NOT NULL DEFAULT 1,
  role_template_id INTEGER,
  credential_preset_id INTEGER,
  suspended INTEGER NOT NULL DEFAULT 0,
  disk_quota_bytes INTEGER,
  max_sessions INTEGER,
  created_at INTEGER NOT NULL,
  last_login_at INTEGER
);
