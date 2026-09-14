package backend

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"

	"github.com/sir-hassan/inframill/pkg/inframill"
)

const instanceVersion = "inframill.dev/instance/v1"
const storageLayout = "uuid-v1"

const commandStoreVersion = 1

type failureDocument struct {
	Code    inframill.ErrorCode `json:"code"`
	Message string              `json:"message"`
	Path    string              `json:"path,omitempty"`
}
type infoDocument struct {
	APIVersion    string                   `json:"api_version"`
	StorageLayout string                   `json:"storage_layout"`
	ID            inframill.InstanceID     `json:"id"`
	Name          string                   `json:"name"`
	Values        inframill.InstanceValues `json:"values"`
	Blueprint     string                   `json:"blueprint"`
	Status        inframill.InstanceStatus `json:"status"`
	Failure       *failureDocument         `json:"failure,omitempty"`
}

func writeInfo(root string, info inframill.InstanceInfo) error {
	doc := infoDocument{APIVersion: instanceVersion, StorageLayout: storageLayout, ID: info.ID, Name: info.Name, Values: info.Values, Blueprint: info.Blueprint, Status: info.Status}
	if info.Failure != nil {
		doc.Failure = &failureDocument{Code: info.Failure.Code, Message: info.Failure.Message}
	}
	content, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(root, "instance.json"), append(content, '\n'))
}

func readInfo(root string, id inframill.InstanceID) (inframill.InstanceInfo, error) {
	var info inframill.InstanceInfo
	content, err := readRegular(root, "instance.json", 1<<20)
	if err != nil {
		return info, err
	}
	var doc infoDocument
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&doc); err != nil {
		return info, errors.New("invalid instance.json")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return info, errors.New("invalid instance.json trailing data")
	}
	if doc.APIVersion != instanceVersion || doc.StorageLayout != storageLayout {
		return info, unsupportedLayout()
	}
	if (id != "" && doc.ID != id) || !inframill.ValidUUID(string(doc.ID)) || !inframill.ValidInstanceName(doc.Name) {
		return info, errors.New("instance.json identity does not match workspace")
	}
	info = inframill.InstanceInfo{ID: doc.ID, Name: doc.Name, Values: doc.Values, Blueprint: doc.Blueprint, Status: doc.Status}
	if doc.Failure != nil {
		info.Failure = &inframill.InstanceFailure{Code: doc.Failure.Code, Message: doc.Failure.Message}
	}
	if doc.Values == nil || doc.Blueprint == "" {
		return info, errors.New("unsupported or incomplete instance.json")
	}
	switch info.Status {
	case inframill.InstanceIdle, inframill.InstanceRunning, inframill.InstanceFailed:
	default:
		return info, errors.New("invalid persisted Instance status")
	}
	return info, nil
}

type requestDocument struct {
	ID             inframill.CommandID `json:"id"`
	Command        string              `json:"command"`
	Args           inframill.Arguments `json:"args"`
	IdempotencyKey string              `json:"idempotency_key,omitempty"`
}
type commandDocument struct {
	Request   requestDocument         `json:"request"`
	Status    inframill.CommandStatus `json:"status"`
	CreatedAt time.Time               `json:"created_at"`
	StartedAt *time.Time              `json:"started_at,omitempty"`
	EndedAt   *time.Time              `json:"ended_at,omitempty"`
	Result    json.RawMessage         `json:"result,omitempty"`
	Error     *failureDocument        `json:"error,omitempty"`
}

func encodeCommand(command inframill.CommandExecution) ([]byte, error) {
	d := commandDocument{Request: requestDocument{ID: command.Request.ID, Command: command.Request.Command, Args: command.Request.Args, IdempotencyKey: command.Request.IdempotencyKey}, Status: command.Status, CreatedAt: command.CreatedAt, StartedAt: command.StartedAt, EndedAt: command.EndedAt, Result: command.Result}
	if command.Error != nil {
		d.Error = &failureDocument{Code: command.Error.Code, Message: command.Error.Message, Path: command.Error.Path}
	}
	return json.Marshal(d)
}

func decodeCommand(data []byte) (inframill.CommandExecution, error) {
	var d commandDocument
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&d); err != nil {
		return inframill.CommandExecution{}, errors.New("invalid stored command JSON")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return inframill.CommandExecution{}, errors.New("invalid stored command JSON")
	}
	if !inframill.ValidUUID(string(d.Request.ID)) || d.Request.Command == "" || d.Request.Args == nil || d.CreatedAt.IsZero() {
		return inframill.CommandExecution{}, errors.New("invalid stored command")
	}
	if d.Status != inframill.Queued && d.Status != inframill.Running && !inframill.Terminal(d.Status) {
		return inframill.CommandExecution{}, errors.New("invalid stored command status")
	}
	c := inframill.CommandExecution{Request: inframill.CommandRequest{ID: d.Request.ID, Command: d.Request.Command, Args: d.Request.Args, IdempotencyKey: d.Request.IdempotencyKey}, Status: d.Status, CreatedAt: d.CreatedAt, StartedAt: d.StartedAt, EndedAt: d.EndedAt, Result: d.Result}
	if d.Error != nil {
		c.Error = &inframill.CommandError{Code: d.Error.Code, Message: d.Error.Message, Path: d.Error.Path}
	}
	return c, nil
}

type commandStore struct{ db *sql.DB }

// validateCommandStore checks the current schema without creating ownership locks,
// changing database settings or persisting an Instance failure.
func validateCommandStore(ctx context.Context, root string) (err error) {
	path := filepath.Join(root, "commands.sqlite")
	if err := regularFile(path); err != nil {
		return err
	}
	uri := url.URL{Scheme: "file", Path: path}
	db, err := sql.Open("sqlite", uri.String()+"?mode=ro")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, db.Close()) }()
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version != commandStoreVersion {
		return errors.New("unsupported command SQLite version; existing data preserved")
	}
	rows, err := db.QueryContext(ctx, "SELECT id, record FROM commands")
	if err != nil {
		return errors.New("invalid command store; existing data preserved")
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		var id string
		var data []byte
		if err := rows.Scan(&id, &data); err != nil {
			return errors.New("invalid command store; existing data preserved")
		}
		command, err := decodeCommand(data)
		if err != nil || string(command.Request.ID) != id {
			return errors.New("invalid command store; existing data preserved")
		}
	}
	return rows.Err()
}

func openStore(ctx context.Context, root string, create bool) (*commandStore, error) {
	path := filepath.Join(root, "commands.sqlite")
	if !create {
		if err := validateCommandStore(ctx, root); err != nil {
			return nil, err
		}
	}
	mode := "rw"
	if create {
		mode = "rwc"
	}
	uri := url.URL{Scheme: "file", Path: path}
	dsn := uri.String() + "?mode=" + mode + "&_pragma=busy_timeout(5000)&_pragma=synchronous(FULL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	fail := func(err error) (*commandStore, error) { return nil, errors.Join(err, db.Close()) }
	var version int
	if checkErr := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); checkErr != nil {
		return fail(checkErr)
	}
	if create {
		if version != 0 {
			return fail(errors.New("database already initialized"))
		}
		_, err = db.ExecContext(ctx, `CREATE TABLE commands(
   sequence INTEGER PRIMARY KEY AUTOINCREMENT,
   id TEXT NOT NULL UNIQUE,
   idempotency_key TEXT,
   status TEXT NOT NULL CHECK(status IN ('queued','running','succeeded','failed','interrupted')),
   record BLOB NOT NULL);
   CREATE UNIQUE INDEX command_keys ON commands(idempotency_key) WHERE idempotency_key IS NOT NULL;
   PRAGMA user_version=1;`)
		if err != nil {
			return fail(err)
		}
	} else if version != commandStoreVersion {
		return fail(errors.New("unsupported command SQLite version"))
	}
	var integrity string
	if err := db.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&integrity); err != nil {
		return fail(err)
	}
	if integrity != "ok" {
		return fail(errors.New("command SQLite integrity check failed"))
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fail(err)
	}
	return &commandStore{db: db}, nil
}

func (s *commandStore) insert(ctx context.Context, c inframill.CommandExecution) error {
	b, err := encodeCommand(c)
	if err != nil {
		return err
	}
	var key any
	if c.Request.IdempotencyKey != "" {
		key = c.Request.IdempotencyKey
	}
	_, err = s.db.ExecContext(ctx, "INSERT INTO commands(id,idempotency_key,status,record) VALUES(?,?,?,?)", string(c.Request.ID), key, string(c.Status), b)
	return err
}

func (s *commandStore) update(ctx context.Context, c inframill.CommandExecution) error {
	b, err := encodeCommand(c)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, "UPDATE commands SET status=?,record=? WHERE id=?", string(c.Status), b, string(c.Request.ID))
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("command record disappeared")
	}
	return nil
}

func (s *commandStore) get(ctx context.Context, id inframill.CommandID) (inframill.CommandExecution, error) {
	var b []byte
	err := s.db.QueryRowContext(ctx, "SELECT record FROM commands WHERE id=?", string(id)).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return inframill.CommandExecution{}, inframill.ErrNotFound
	}
	if err != nil {
		return inframill.CommandExecution{}, err
	}
	c, err := decodeCommand(b)
	if err == nil && c.Request.ID != id {
		return inframill.CommandExecution{}, errors.New("command identity mismatch")
	}
	return c, err
}

func (s *commandStore) byKey(ctx context.Context, key string) (inframill.CommandExecution, error) {
	var b []byte
	err := s.db.QueryRowContext(ctx, "SELECT record FROM commands WHERE idempotency_key=?", key).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return inframill.CommandExecution{}, inframill.ErrNotFound
	}
	if err != nil {
		return inframill.CommandExecution{}, err
	}
	return decodeCommand(b)
}

func (s *commandStore) interrupt(ctx context.Context, now time.Time) error {
	rows, checkErr := s.db.QueryContext(ctx, "SELECT record FROM commands WHERE status IN ('queued','running')")
	if checkErr != nil {
		return checkErr
	}
	defer rows.Close() //nolint:errcheck // Explicit closes below report errors; this handles early returns.
	var commands []inframill.CommandExecution
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			_ = rows.Close() //nolint:errcheck // Preserve the original query/decode failure.
			return err
		}
		c, err := decodeCommand(data)
		if err != nil {
			_ = rows.Close() //nolint:errcheck // Preserve the original query/decode failure.
			return err
		}
		commands = append(commands, c)
	}
	checkErr = errors.Join(rows.Err(), rows.Close())
	if checkErr != nil {
		return checkErr
	}
	tx, checkErr := s.db.BeginTx(ctx, nil)
	if checkErr != nil {
		return checkErr
	}
	defer tx.Rollback() //nolint:errcheck // Rollback is harmless after a committed transaction.
	for _, c := range commands {
		c.Status = inframill.Interrupted
		c.EndedAt = &now
		c.Error = &inframill.CommandError{Code: inframill.CommandInterrupted, Message: "execution interrupted; work is never replayed"}
		b, err := encodeCommand(c)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE commands SET status=?,record=? WHERE id=?", string(c.Status), b, string(c.Request.ID)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func stateError(action string, err error) error {
	return &inframill.Error{Code: inframill.StateFailed, Message: fmt.Sprintf("%s: %v", action, err), Cause: err}
}
