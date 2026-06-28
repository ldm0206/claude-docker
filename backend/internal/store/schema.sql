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

CREATE TABLE IF NOT EXISTS role_templates (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT UNIQUE NOT NULL,
  disk_quota_bytes INTEGER NOT NULL,
  cpu_quota TEXT NOT NULL,
  memory_max_bytes INTEGER NOT NULL,
  max_sessions INTEGER NOT NULL,
  permissions TEXT NOT NULL DEFAULT '{}',
  created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS credential_presets (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT UNIQUE NOT NULL,
  encrypted_blob BLOB NOT NULL,
  note TEXT,
  created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS sessions (
  id TEXT PRIMARY KEY,
  user_id INTEGER NOT NULL,
  name TEXT,
  started_at INTEGER NOT NULL,
  last_seen_at INTEGER NOT NULL,
  alive INTEGER NOT NULL DEFAULT 1
);
