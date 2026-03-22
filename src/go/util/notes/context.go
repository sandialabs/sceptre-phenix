package notes

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/gofrs/uuid/v5"

	"phenix/util/plog"
)

type (
	noteUUID  struct{}
	noteFlush struct{}
)

type warn struct {
	note error
	seen bool
}

type info struct {
	note string
	seen bool
}

var (
	errs     = make(map[string][]warn) //nolint:gochecknoglobals // package level notes storage
	warnings = make(map[string][]warn) //nolint:gochecknoglobals // package level notes storage
	infos    = make(map[string][]info) //nolint:gochecknoglobals // package level notes storage

	errsMutex     sync.RWMutex //nolint:gochecknoglobals // package level notes storage
	warningsMutex sync.RWMutex //nolint:gochecknoglobals // package level notes storage
	infosMutex    sync.RWMutex //nolint:gochecknoglobals // package level notes storage
)

func Context(ctx context.Context, flush bool) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}

	uuid := uuid.Must(uuid.NewV4()).String()
	ctx = context.WithValue(ctx, noteUUID{}, uuid)

	return context.WithValue(ctx, noteFlush{}, flush)
}

func AddErrors(ctx context.Context, flush bool, e ...error) {
	uuid, ok := ctx.Value(noteUUID{}).(string)

	if !ok {
		return
	}

	flushable, _ := ctx.Value(noteFlush{}).(bool)

	errsMutex.Lock()
	defer errsMutex.Unlock()

	for _, n := range e {
		note := warn{note: n} //nolint:exhaustruct // partial initialization

		if flushable && flush {
			plog.Error(plog.TypeSystem, n.Error())
			note.seen = true
		} else {
			note.seen = false
		}

		errs[uuid] = append(errs[uuid], note)
	}
}

func AddWarnings(ctx context.Context, flush bool, w ...error) {
	uuid, ok := ctx.Value(noteUUID{}).(string)

	if !ok {
		return
	}

	flushable, _ := ctx.Value(noteFlush{}).(bool)

	warningsMutex.Lock()
	defer warningsMutex.Unlock()

	for _, n := range w {
		note := warn{note: n} //nolint:exhaustruct // partial initialization

		if flushable && flush {
			plog.Warn(plog.TypeSystem, n.Error())
			note.seen = true
		} else {
			note.seen = false
		}

		warnings[uuid] = append(warnings[uuid], note)
	}
}

func AddInfo(ctx context.Context, flush bool, i ...string) {
	uuid, ok := ctx.Value(noteUUID{}).(string)

	if !ok {
		return
	}

	flushable, _ := ctx.Value(noteFlush{}).(bool)

	infosMutex.Lock()
	defer infosMutex.Unlock()

	for _, n := range i {
		note := info{note: n} //nolint:exhaustruct // partial initialization

		if flushable && flush {
			plog.Info(plog.TypeSystem, n)
			note.seen = true
		} else {
			note.seen = false
		}

		infos[uuid] = append(infos[uuid], note)
	}
}

func ClearErrors(ctx context.Context) {
	uuid, ok := ctx.Value(noteUUID{}).(string)

	if !ok {
		return
	}

	errsMutex.Lock()
	defer errsMutex.Unlock()

	delete(errs, uuid)
}

func ClearWarnings(ctx context.Context) {
	uuid, ok := ctx.Value(noteUUID{}).(string)

	if !ok {
		return
	}

	warningsMutex.Lock()
	defer warningsMutex.Unlock()

	delete(warnings, uuid)
}

func ClearInfo(ctx context.Context) {
	uuid, ok := ctx.Value(noteUUID{}).(string)

	if !ok {
		return
	}

	infosMutex.Lock()
	defer infosMutex.Unlock()

	delete(infos, uuid)
}

func Errors(ctx context.Context, all bool) []error {
	uuid, ok := ctx.Value(noteUUID{}).(string)

	if !ok {
		return nil
	}

	errsMutex.RLock()
	defer errsMutex.RUnlock()

	if all {
		e := make([]error, len(errs[uuid]))

		for idx, err := range errs[uuid] {
			e[idx] = err.note

			err.seen = true
			errs[uuid][idx] = err
		}

		return e
	}

	var e []error

	for idx, err := range errs[uuid] {
		if err.seen {
			continue
		}

		e = append(e, err.note)

		err.seen = true
		errs[uuid][idx] = err
	}

	return e
}

func Warnings(ctx context.Context, all bool) []error {
	uuid, ok := ctx.Value(noteUUID{}).(string)

	if !ok {
		return nil
	}

	warningsMutex.RLock()
	defer warningsMutex.RUnlock()

	if all {
		w := make([]error, len(warnings[uuid]))

		for idx, err := range warnings[uuid] {
			w[idx] = err.note

			err.seen = true
			warnings[uuid][idx] = err
		}

		return w
	}

	var w []error

	for idx, err := range warnings[uuid] {
		if err.seen {
			continue
		}

		w = append(w, err.note)

		err.seen = true
		warnings[uuid][idx] = err
	}

	return w
}

func Info(ctx context.Context, all bool) []string {
	uuid, ok := ctx.Value(noteUUID{}).(string)

	if !ok {
		return nil
	}

	infosMutex.RLock()
	defer infosMutex.RUnlock()

	if all {
		i := make([]string, len(infos[uuid]))

		for idx, info := range infos[uuid] {
			i[idx] = info.note

			info.seen = true
			infos[uuid][idx] = info
		}

		return i
	}

	var i []string

	for idx, info := range infos[uuid] {
		if info.seen {
			continue
		}

		i = append(i, info.note)

		info.seen = true
		infos[uuid][idx] = info
	}

	return i
}

func PrettyPrint(ctx context.Context, all bool) {
	uuid, ok := ctx.Value(noteUUID{}).(string)

	if !ok {
		return
	}

	errsMutex.RLock()
	defer errsMutex.RUnlock()

	for _, e := range errs[uuid] {
		if all || !e.seen {
			plog.Error(plog.TypeSystem, e.note.Error())
		}
	}

	warningsMutex.RLock()
	defer warningsMutex.RUnlock()

	for _, w := range warnings[uuid] {
		if all || !w.seen {
			plog.Warn(plog.TypeSystem, w.note.Error())
		}
	}

	infosMutex.RLock()
	defer infosMutex.RUnlock()

	for _, i := range infos[uuid] {
		if all || !i.seen {
			plog.Info(plog.TypeSystem, i.note)
		}
	}
}

func ToJSON(ctx context.Context) json.RawMessage {
	uuid, ok := ctx.Value(noteUUID{}).(string)

	if !ok {
		return nil
	}

	body := make(map[string][]string)

	errsMutex.RLock()
	defer errsMutex.RUnlock()

	if v, ok := errs[uuid]; ok {
		s := make([]string, len(v))

		for i, e := range v {
			s[i] = e.note.Error()
		}

		body["errors"] = s
	}

	warningsMutex.RLock()
	defer warningsMutex.RUnlock()

	if v, ok := warnings[uuid]; ok {
		s := make([]string, len(v))

		for i, e := range v {
			s[i] = e.note.Error()
		}

		body["warnings"] = s
	}

	infosMutex.RLock()
	defer infosMutex.RUnlock()

	if v, ok := infos[uuid]; ok {
		s := make([]string, len(v))

		for i, e := range v {
			s[i] = e.note
		}

		body["infos"] = s
	}

	raw, _ := json.Marshal(body)

	return raw
}
