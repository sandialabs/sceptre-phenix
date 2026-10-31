package broker

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"sync"
	"sync/atomic"

	"phenix/util/mm"
	"phenix/util/plog"
	bt "phenix/web/broker/brokertypes"
	"phenix/web/util"
)

// shotKey names one screenshot: a VM at a width.
type shotKey struct {
	exp   string
	vm    string
	width string
}

// vmKey names a VM in a client's view.
type vmKey struct {
	exp  string
	name string
}

type pngHash = [sha256.Size]byte

// frame is a screenshot as sent to clients, encoded once for every client
// showing that VM at that width.
type frame struct {
	hash pngHash
	msg  encodedPublish
}

// frameCache keeps the frame last taken of each screenshot in view, so a
// screenshot whose pixels have not changed is not encoded again.
type frameCache struct {
	mu     sync.Mutex
	frames map[shotKey]frame
}

var (
	frames = frameCache{mu: sync.Mutex{}, frames: make(map[shotKey]frame)} //nolint:gochecknoglobals // shared by every client
	// set while a broker-wide screenshot round runs; a tick during one is skipped.
	shotRound atomic.Bool //nolint:gochecknoglobals // global state
)

// get returns the frame of key's screenshot, taking it (or reading it from
// the screenshot cache) and encoding it only when its pixels changed.
func (fc *frameCache) get(ctx context.Context, key shotKey) (frame, error) {
	png, err := deps.screenshot(ctx, key.exp, key.vm, key.width)
	if err != nil {
		return frame{}, err
	}

	hash := sha256.Sum256(png)

	fc.mu.Lock()
	cached, ok := fc.frames[key]
	fc.mu.Unlock()

	if ok && cached.hash == hash {
		return cached, nil
	}

	msg, err := encodeFrame(key, png)
	if err != nil {
		return frame{}, err
	}

	f := frame{hash: hash, msg: msg}

	fc.mu.Lock()
	fc.frames[key] = f
	fc.mu.Unlock()

	return f, nil
}

// retain drops the frames of screenshots no longer in any client's view.
func (fc *frameCache) retain(wanted map[shotKey][]*Client) {
	fc.mu.Lock()
	defer fc.mu.Unlock()

	maps.DeleteFunc(fc.frames, func(key shotKey, _ frame) bool {
		_, ok := wanted[key]

		return !ok
	})
}

// encodeFrame builds the experiment/vm/screenshot message for png.
func encodeFrame(key shotKey, png []byte) (encodedPublish, error) {
	encoded := "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)

	body, err := json.Marshal(util.WithRoot("screenshot", encoded))
	if err != nil {
		return encodedPublish{}, err //nolint:wrapcheck // logged by the caller
	}

	out, ok := encode(bt.Publish{ //nolint:exhaustruct // sent to the clients showing the VM, not broadcast
		Resource: bt.NewResource("experiment/vm/screenshot", key.exp+"/"+key.vm, "update"),
		Result:   body,
	})
	if !ok {
		return encodedPublish{}, errors.New("encoding screenshot message")
	}

	return out, nil
}

// startScreenshotRound sends every client its in-view screenshots on a new goroutine, unless the
// last round still runs. Only the broker loop may call it, as it reads the clients map.
func startScreenshotRound(ctx context.Context) {
	if !shotRound.CompareAndSwap(false, true) {
		return
	}

	viewers := slices.Collect(maps.Keys(clients))

	go func() {
		defer shotRound.Store(false)

		wanted := wantedShots(viewers)
		frames.retain(wanted)
		pushFrames(ctx, wanted)
	}()
}

// wantedShots returns the screenshots the clients have in view, each with the
// clients showing it.
func wantedShots(viewers []*Client) map[shotKey][]*Client {
	wanted := make(map[shotKey][]*Client)

	for _, c := range viewers {
		if isClosed(c.done) {
			continue
		}

		width := c.screenshotSize()

		c.vmMu.RLock()

		for _, v := range c.vms {
			if v.running {
				key := shotKey{exp: v.exp, vm: v.name, width: width}
				wanted[key] = append(wanted[key], c)
			}
		}

		c.vmMu.RUnlock()
	}

	return wanted
}

// pushFrames sends each wanted screenshot, taken and encoded once, to the clients showing it
// until ctx is done, skipping a client already sent that frame for that VM.
func pushFrames(ctx context.Context, wanted map[shotKey][]*Client) {
	keys := slices.SortedFunc(maps.Keys(wanted), func(a, b shotKey) int {
		return cmp.Or(cmp.Compare(a.exp, b.exp), cmp.Compare(a.vm, b.vm), cmp.Compare(a.width, b.width))
	})

	for _, key := range keys {
		if ctx.Err() != nil {
			return
		}

		live := slices.DeleteFunc(wanted[key], func(c *Client) bool { return isClosed(c.done) })
		if len(live) == 0 {
			continue
		}

		f, err := frames.get(ctx, key)
		if err != nil {
			if ctx.Err() != nil {
				return
			}

			if !errors.Is(err, mm.ErrVMNotFound) && !errors.Is(err, mm.ErrScreenshotNotFound) {
				plog.Error(plog.TypeSystem, "getting screenshot for WebSocket client", "err", err)
			}

			continue
		}

		for _, c := range live {
			c.sendFrame(key, f)
		}
	}
}

// sendFrame queues f unless it was the VM's last frame sent or the client no longer shows the VM
// running at that width. A client whose queue is full gets the next round's frame instead.
func (c *Client) sendFrame(key shotKey, f frame) {
	if c.screenshotSize() != key.width {
		return
	}

	vm := vmKey{exp: key.exp, name: key.vm}

	c.vmMu.Lock()
	defer c.vmMu.Unlock()

	if !slices.Contains(c.vms, vmScope{exp: key.exp, name: key.vm, running: true}) {
		return
	}

	if last, ok := c.sent[vm]; ok && last == f.hash {
		return
	}

	select {
	case c.publish <- f.msg:
		if c.sent == nil {
			c.sent = make(map[vmKey]pngHash)
		}

		c.sent[vm] = f.hash
	default:
	}
}
