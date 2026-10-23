-- E2 per-match summary; the console lists matches by start time.
CREATE TABLE match_result (
  id             BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
  room           VARCHAR(64)     NOT NULL,
  started_at     DATETIME(6)     NOT NULL,
  ended_at       DATETIME(6)     NOT NULL,
  peak_players   INT UNSIGNED    NOT NULL,
  frames         BIGINT UNSIGNED NOT NULL,
  overrun_frames BIGINT UNSIGNED NOT NULL,
  KEY started_at (started_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
