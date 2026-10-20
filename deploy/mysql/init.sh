#!/bin/sh
set -eu
export MYSQL_PWD="$(cat /run/secrets/mysql_root_password)"
app_password=$(cat /run/secrets/mysql_app_password)
mysql -uroot <<SQL
CREATE DATABASE IF NOT EXISTS bcl;
CREATE USER IF NOT EXISTS 'bcl'@'%' IDENTIFIED WITH caching_sha2_password BY '$app_password';
GRANT ALL ON bcl.* TO 'bcl'@'%';
GRANT REPLICATION SLAVE, REPLICATION CLIENT ON *.* TO 'bcl'@'%';
USE bcl;
CREATE TABLE IF NOT EXISTS bench_run (
 run_id VARCHAR(64) PRIMARY KEY, story VARCHAR(32) NOT NULL, arm VARCHAR(64) NOT NULL,
 repeat_no INT NOT NULL, git_commit CHAR(40) NOT NULL, data_version VARCHAR(128) NOT NULL,
 env_json JSON NOT NULL, result_path VARCHAR(1024) NOT NULL, status VARCHAR(32) NOT NULL,
 started_at DATETIME(6) NOT NULL, KEY story_arm_repeat(story,arm,repeat_no)
);
CREATE TABLE IF NOT EXISTS p0_binlog(id BIGINT PRIMARY KEY, version BIGINT NOT NULL);
SQL
