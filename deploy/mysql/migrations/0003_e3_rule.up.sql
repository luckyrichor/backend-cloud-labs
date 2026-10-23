-- E3 rule versions; a name never reuses a version number.
CREATE TABLE rule (
  id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
  name       VARCHAR(128)    NOT NULL,
  expr       VARCHAR(4096)   NOT NULL,
  version    INT UNSIGNED    NOT NULL,
  created_at DATETIME(6)     NOT NULL,
  UNIQUE KEY name_version (name, version)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
