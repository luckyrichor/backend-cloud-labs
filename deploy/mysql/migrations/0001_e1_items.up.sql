-- E1 product source (S1/S2). Primary key only: E1 reads by id, writes by id, batch-reads by ids.
-- version is bumped in the writing transaction; values above 2^53 stay exact as BIGINT UNSIGNED.
CREATE TABLE items (
  id          BIGINT UNSIGNED NOT NULL PRIMARY KEY,
  title       VARCHAR(512)    NOT NULL,
  category_id INT UNSIGNED    NOT NULL,
  price_cent  BIGINT          NOT NULL,
  stock       INT             NOT NULL,
  version     BIGINT UNSIGNED NOT NULL,
  updated_at  DATETIME(6)     NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
