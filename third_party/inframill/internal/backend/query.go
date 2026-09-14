package backend

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/sir-hassan/inframill/pkg/inframill"
)

func (i *instance) queryError(ctx context.Context, err error) error {
	if err == nil || errors.Is(err, inframill.ErrNotFound) {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	select {
	case <-i.finished:
		if i.info.Status == inframill.InstanceFailed {
			return inframill.ErrInstanceFailed
		}
		return inframill.ErrClosed
	default:
	}
	i.failLocked(stateError("read command storage", err))
	return inframill.ErrInstanceFailed
}

func (i *instance) GetCommandExecution(ctx context.Context, id inframill.CommandID) (inframill.CommandExecution, error) {
	if !inframill.ValidUUID(string(id)) {
		return inframill.CommandExecution{}, inframill.InvalidArgument("command_id", "command_id must be a UUID")
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.deleted {
		return inframill.CommandExecution{}, inframill.ErrNotFound
	}
	if i.store == nil {
		return inframill.CommandExecution{}, inframill.ErrInstanceFailed
	}
	c, err := i.store.get(ctx, id)
	return c, i.queryError(ctx, err)
}

type commandCursor struct {
	InstanceID inframill.InstanceID    `json:"instance_id"`
	Status     inframill.CommandStatus `json:"status"`
	Sequence   int64                   `json:"sequence"`
}

func (i *instance) ListCommandExecutions(ctx context.Context, q inframill.CommandExecutionQuery) (inframill.CommandExecutionPage, error) {
	page := inframill.CommandExecutionPage{Executions: []inframill.CommandExecution{}, ObservedAt: i.backend.clock().UTC()}
	limit := q.Limit
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 100 {
		return page, inframill.InvalidArgument("limit", "limit must be 1..100")
	}
	if q.Status != "" && q.Status != inframill.Queued && q.Status != inframill.Running && !inframill.Terminal(q.Status) {
		return page, inframill.InvalidArgument("status", "invalid command status")
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.deleted {
		return page, inframill.ErrNotFound
	}
	if i.store == nil {
		return page, inframill.ErrInstanceFailed
	}
	before := int64(9223372036854775807)
	if q.Cursor != "" {
		data, err := base64.RawURLEncoding.DecodeString(q.Cursor)
		var cursor commandCursor
		if err != nil || json.Unmarshal(data, &cursor) != nil || cursor.InstanceID != i.info.ID || cursor.Status != q.Status || cursor.Sequence < 1 {
			return page, inframill.InvalidArgument("cursor", "invalid cursor for this Instance and filter")
		}
		var seq int64
		err = i.store.db.QueryRowContext(ctx, "SELECT sequence FROM commands WHERE sequence=? AND (?='' OR status=?)", cursor.Sequence, string(q.Status), string(q.Status)).Scan(&seq)
		if errors.Is(err, sql.ErrNoRows) {
			return page, inframill.InvalidArgument("cursor", "cursor no longer matches command history")
		}
		if err != nil {
			return page, i.queryError(ctx, err)
		}
		before = seq
	}
	rows, err := i.store.db.QueryContext(ctx, "SELECT sequence,record FROM commands WHERE sequence<? AND (?='' OR status=?) ORDER BY sequence DESC LIMIT ?", before, string(q.Status), string(q.Status), limit+1)
	if err != nil {
		return page, i.queryError(ctx, err)
	}
	defer rows.Close() //nolint:errcheck // Explicit closes below report errors; this handles early returns.
	var last int64
	for rows.Next() {
		var seq int64
		var data []byte
		if checkErr := rows.Scan(&seq, &data); checkErr != nil {
			_ = rows.Close() //nolint:errcheck // Preserve the original query/decode failure.
			return page, i.queryError(ctx, checkErr)
		}
		if len(page.Executions) == limit {
			cursorData, _ := json.Marshal(commandCursor{InstanceID: i.info.ID, Status: q.Status, Sequence: last}) //nolint:errcheck,errchkjson // Cursor contains only strings and integers.
			page.NextCursor = base64.RawURLEncoding.EncodeToString(cursorData)
			break
		}
		c, checkErr := decodeCommand(data)
		if checkErr != nil {
			_ = rows.Close() //nolint:errcheck // Preserve the original query/decode failure.
			return page, i.queryError(ctx, checkErr)
		}
		page.Executions = append(page.Executions, c)
		last = seq
	}
	err = errors.Join(rows.Err(), rows.Close())
	return page, i.queryError(ctx, err)
}

func (i *instance) Stdout(id inframill.CommandID) (io.ReadCloser, error) {
	return i.log(id, "stdout.log")
}

func (i *instance) Stderr(id inframill.CommandID) (io.ReadCloser, error) {
	return i.log(id, "stderr.log")
}

func (i *instance) log(id inframill.CommandID, name string) (io.ReadCloser, error) {
	if !inframill.ValidUUID(string(id)) {
		return nil, inframill.InvalidArgument("command_id", "command_id must be a UUID")
	}
	root := filepath.Join(i.root, "logs")
	if err := directory(root); err != nil {
		if os.IsNotExist(err) {
			return nil, inframill.ErrNotFound
		}
		return nil, err
	}
	commandRoot := filepath.Join(root, string(id))
	if err := directory(commandRoot); err != nil {
		if os.IsNotExist(err) {
			return nil, inframill.ErrNotFound
		}
		return nil, err
	}
	if err := regularFile(filepath.Join(commandRoot, name)); err != nil {
		if os.IsNotExist(err) {
			return nil, inframill.ErrNotFound
		}
		return nil, err
	}
	dir, err := os.OpenRoot(commandRoot)
	if err != nil {
		return nil, err
	}
	defer dir.Close() //nolint:errcheck // Directory handle is not used by the independent reader.
	f, err := dir.Open(name)
	if err != nil {
		return nil, err
	}
	stat, err := f.Stat()
	if err != nil {
		return nil, errors.Join(err, f.Close())
	}
	return snapshotReader{SectionReader: io.NewSectionReader(f, 0, stat.Size()), file: f}, nil
}
