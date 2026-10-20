package loadgen

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	_ "github.com/go-sql-driver/mysql"
)

// Registry never returns a DSN or database diagnostic containing credentials.
func Register(ctx context.Context, dsn, id, story, arm, commit, dataVersion, resultPath string, result Result) error {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return errors.New("registry configuration rejected")
	}
	defer db.Close()
	if err = db.PingContext(ctx); err != nil {
		return errors.New("registry connection failed")
	}
	env, _ := json.Marshal(map[string]any{"protocol": result.Protocol, "scope": result.Scope, "calibration_only": story == "calibration"})
	status := "completed"
	if !result.Valid {
		status = "invalid"
	}
	_, err = db.ExecContext(ctx, `INSERT INTO bench_run(run_id,story,arm,repeat_no,git_commit,data_version,env_json,result_path,status,started_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, id, story, arm, 1, commit, dataVersion, string(env), resultPath, status, result.StartedAt)
	if err != nil {
		return errors.New("registry insert failed")
	}
	return nil
}
