package web

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"phenix/web/broker"

	bt "phenix/web/broker/brokertypes"

	"github.com/hpcloud/tail"
)

var mmLogRegex = regexp.MustCompile(`\A(\d{4}\/\d{2}\/\d{2} \d{2}:\d{2}:\d{2})\s* (DEBUG|INFO|WARN|WARNING|ERROR|FATAL) .*?: (.*)\z`)

func PublishMinimegaLogs(ctx context.Context, minimega string) {
	if minimega == "" {
		return
	}

	logs := make(chan string)

	mmLogs, err := tail.TailFile(minimega, tail.Config{Follow: true, ReOpen: true, Poll: true})
	if err != nil {
		panic("setting up tail for minimega logs: " + err.Error())
	}

	go func() {
		for l := range mmLogs.Lines {
			logs <- l.Text
		}
	}()

	// used to detect multi-line minimega logs
	var body map[string]interface{}

	for {
		select {
		case <-ctx.Done():
			return
		case log := <-logs:
			parts := mmLogRegex.FindStringSubmatch(log)

			if len(parts) == 4 {
				ts, err := time.ParseInLocation("2006/01/02 15:04:05", parts[1], time.Local)
				if err != nil {
					continue
				}

				body = map[string]interface{}{
					"source":    "minimega",
					"timestamp": parts[1],
					"epoch":     ts.Unix(),
					"level":     parts[2],
					"log":       parts[3],
				}
			} else if body != nil {
				body["log"] = log
			} else {
				continue
			}

			marshalled, _ := json.Marshal(body)

			broker.Broadcast(
				nil,
				bt.NewResource("log", "minimega", "update"),
				marshalled,
			)
		}
	}
}

func PublishPhenixLog(ts time.Time, level, log string) {
	tstamp := ts.Local().Format("2006/01/02 15:04:05")

	body := map[string]interface{}{
		"source":    "phenix",
		"timestamp": tstamp,
		"epoch":     ts.Unix(),
		"level":     strings.ToUpper(level),
		"log":       log,
	}

	marshalled, _ := json.Marshal(body)

	broker.Broadcast(
		nil,
		bt.NewResource("log", "phenix", "update"),
		marshalled,
	)
}
