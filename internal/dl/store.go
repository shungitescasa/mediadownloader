package dl

import (
	"context"
	"database/sql"
	"errors"
	"time"

	_ "modernc.org/sqlite"
)

type TaskType uint8

const (
	TypeVideo TaskType = iota
	TypeAudio
	TypeMeta
)

type TaskStatus uint8

const (
	StatusPending TaskStatus = iota
	StatusDownloading
	StatusDone
	StatusFailed
)

var (
	typeNames   = [...]string{"video", "audio", "meta"}
	statusNames = [...]string{"pending", "downloading", "done", "failed"}
)

func (t TaskType) String() string {
	if int(t) < len(typeNames) {
		return typeNames[t]
	}
	return "unknown"
}

func (t TaskType) MarshalText() ([]byte, error) {
	return []byte(t.String()), nil
}

func parseType(s string) (TaskType, bool) {
	for i, name := range typeNames {
		if name == s {
			return TaskType(i), true
		}
	}
	return 0, false
}

func (s TaskStatus) String() string {
	if int(s) < len(statusNames) {
		return statusNames[s]
	}
	return "unknown"
}

func (s TaskStatus) MarshalText() ([]byte, error) {
	return []byte(s.String()), nil
}

type QueueTask struct {
	ID          string     `json:"id"`
	URL         string     `json:"url"`
	Type        TaskType   `json:"type"`
	Status      TaskStatus `json:"status"`
	Description string     `json:"description,omitempty"`
	Extension   string     `json:"-"`
	MimeType    string     `json:"-"`
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   time.Time  `json:"expires_at"`
}

const schema = `
CREATE TABLE IF NOT EXISTS tasks (
	id          TEXT PRIMARY KEY,
	url         TEXT NOT NULL,
	type        INTEGER NOT NULL,
	status      INTEGER NOT NULL,
	description TEXT NOT NULL DEFAULT '',
	extension   TEXT NOT NULL DEFAULT '',
	mime_type   TEXT NOT NULL DEFAULT '',
	created_at  INTEGER NOT NULL,
	expires_at  INTEGER NOT NULL
) STRICT;
CREATE INDEX IF NOT EXISTS tasks_status ON tasks (status);
CREATE INDEX IF NOT EXISTS tasks_expires_at ON tasks (expires_at);
`

const taskColumns = "id, url, type, status, description, extension, mime_type, created_at, expires_at"

var db *sql.DB

type querier interface {
	QueryRow(query string, args ...any) *sql.Row
	Query(query string, args ...any) (*sql.Rows, error)
	Exec(query string, args ...any) (sql.Result, error)
}

func OpenStore(path string) error {
	conn, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return err
	}
	conn.SetMaxOpenConns(1)

	if _, err := conn.ExecContext(context.Background(), schema); err != nil {
		conn.Close()
		return err
	}
	db = conn
	return nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanTask(sc scanner) (QueueTask, error) {
	var t QueueTask
	var created, expires int64
	err := sc.Scan(&t.ID, &t.URL, &t.Type, &t.Status, &t.Description, &t.Extension, &t.MimeType, &created, &expires)
	t.CreatedAt = time.Unix(created, 0)
	t.ExpiresAt = time.Unix(expires, 0)
	return t, err
}

func getTask(q querier, id string) (QueueTask, bool, error) {
	t, err := scanTask(q.QueryRow(
		"SELECT "+taskColumns+" FROM tasks WHERE id = ? AND expires_at > ?",
		id, time.Now().Unix(),
	))
	if errors.Is(err, sql.ErrNoRows) {
		return QueueTask{}, false, nil
	}
	return t, err == nil, err
}

func findTask(id string) (QueueTask, bool, error) {
	return getTask(db, id)
}

func listTasks(q querier) ([]QueueTask, error) {
	rows, err := q.Query("SELECT " + taskColumns + " FROM tasks ORDER BY created_at, rowid")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []QueueTask
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

func purgeExpired(q querier) error {
	_, err := q.Exec("DELETE FROM tasks WHERE expires_at <= ?", time.Now().Unix())
	return err
}
