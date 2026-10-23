-- E5 CMDB. change_event is the only source of truth; ci/relation are replayable views and
-- snapshot_meta indexes periodic snapshots. Rows carry [created_rev, deleted_rev) intervals.
CREATE TABLE tenant (
  id   BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
  name VARCHAR(64)     NOT NULL,
  UNIQUE KEY name (name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE user_account (
  id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
  tenant_id     BIGINT UNSIGNED NOT NULL,
  name          VARCHAR(64)     NOT NULL,
  password_hash VARCHAR(255)    NOT NULL,
  disabled      BOOLEAN         NOT NULL DEFAULT FALSE,
  UNIQUE KEY tenant_name (tenant_id, name),
  CONSTRAINT user_tenant FOREIGN KEY (tenant_id) REFERENCES tenant (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE role_binding (
  id              BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
  user_id         BIGINT UNSIGNED NOT NULL,
  role            ENUM('viewer','operator','admin') NOT NULL,
  scope_tenant_id BIGINT UNSIGNED NOT NULL,
  KEY user_id (user_id),
  CONSTRAINT binding_user FOREIGN KEY (user_id) REFERENCES user_account (id),
  CONSTRAINT binding_tenant FOREIGN KEY (scope_tenant_id) REFERENCES tenant (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- live is 1 while the CI exists and NULL after deletion. The plan's unique (tenant, kind, name)
-- would forbid re-creating a deleted name; NULLs never collide, so uniqueness only binds live rows.
CREATE TABLE ci (
  id          BIGINT UNSIGNED NOT NULL PRIMARY KEY,
  tenant_id   BIGINT UNSIGNED NOT NULL,
  kind        VARCHAR(32)     NOT NULL,
  name        VARCHAR(128)    NOT NULL,
  attributes  JSON            NOT NULL,
  created_rev BIGINT UNSIGNED NOT NULL,
  deleted_rev BIGINT UNSIGNED NULL,
  live        TINYINT AS (IF(deleted_rev IS NULL, 1, NULL)) STORED,
  UNIQUE KEY tenant_kind_name_live (tenant_id, kind, name, live),
  KEY deleted_rev (deleted_rev)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- No foreign keys to ci: a relation's history may outlive the CI rows it is replayed against.
CREATE TABLE relation (
  id          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
  from_id     BIGINT UNSIGNED NOT NULL,
  to_id       BIGINT UNSIGNED NOT NULL,
  kind        VARCHAR(32)     NOT NULL,
  tenant_id   BIGINT UNSIGNED NOT NULL,
  created_rev BIGINT UNSIGNED NOT NULL,
  deleted_rev BIGINT UNSIGNED NULL,
  KEY from_kind (from_id, kind),
  KEY to_kind (to_id, kind)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE change_event (
  rev           BIGINT UNSIGNED NOT NULL PRIMARY KEY,
  tenant_id     BIGINT UNSIGNED NOT NULL,
  actor_user_id BIGINT UNSIGNED NOT NULL,
  reason        VARCHAR(512)    NOT NULL,
  operation     VARCHAR(32)     NOT NULL,
  payload       JSON            NOT NULL,
  ts            DATETIME(6)     NOT NULL,
  KEY tenant_rev (tenant_id, rev)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE snapshot_meta (
  rev        BIGINT UNSIGNED NOT NULL PRIMARY KEY,
  file_path  VARCHAR(1024)   NOT NULL,
  sha256     CHAR(64)        NOT NULL,
  created_at DATETIME(6)     NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
