package plog

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

var(
	haveWarnedLoggingFormat = false
)

// This method creates a named pipe for a subprocess to write logs to
// The pipe is read and logs are logged using plog
// It is recommended that the subprocess log to the pipe file in json with 'level' and 'msg' keys (additional keys allowed).
// Otherwise the entire line will be treated as a log message
// callers should close the returned channel after the subprocess finishes to ensure cleanup
func ReadProcessLogs(logPipePath string, logType LogType, attrs ...any) (chan<- struct{}, error) {

	if err := os.MkdirAll(filepath.Dir(logPipePath), 0755); err != nil {
        return nil, fmt.Errorf("error creating directory for app logs [%s]: %w", logPipePath, err)
    }
	os.Remove(logPipePath)
	err :=  syscall.Mkfifo(logPipePath, 0644)
	if err != nil {
		return nil, fmt.Errorf("error creating file for process logs [%s]: %w", logPipePath, err)
	}

	readDone := make(chan struct{})
	processDone := make(chan struct{})

	go func() {
		logFile, err := os.OpenFile(logPipePath, os.O_RDONLY, os.ModeNamedPipe)
		if err != nil {
			Error(logType, "error opening named pipe for reading logs", attrs...)
			return
		}
		defer logFile.Close()
		defer os.Remove(logPipePath)
		defer close(readDone)

		scanner := bufio.NewScanner(logFile)
		for scanner.Scan() {
			log := scanner.Bytes()
			parts := map[string]interface{}{}
			err := json.Unmarshal(scanner.Bytes(), &parts)
			if err != nil {
				if !haveWarnedLoggingFormat {
					Warn(logType, "For best experience, modify process to log as json with 'level' and 'msg' keys", attrs...)
					haveWarnedLoggingFormat = true
				}
				Info(logType, string(log), attrs...)
			} else {
				levelStr, ok := parts["level"]
				if !ok {
					Warn(logType, "could not find level key in log", append([]any{"log", string(log)}, attrs...)...)
					Info(logType, string(log), attrs...)
					continue
				}
				msg, ok := parts["msg"]
				if !ok {
					Warn(logType, "could not find msg key in log",  append([]any{"log", string(log)}, attrs...)...)
					Info(logType, string(log), attrs...)
					continue
				}

				// prevent time interfering with plog
				if t, ok := parts["time"]; ok {
					parts["proc_time"] = t
					delete(parts, "time")
				}
							
				delete(parts, "msg")
				delete(parts, "level")
				extras := attrs[:]
				for k, v := range parts {
					extras = append(extras, k, v)
				}

				Log(TextToLevel(levelStr.(string)), logType, msg.(string), extras...)
			}
		}
		err = scanner.Err()
		if err != nil {
			Error(logType, "error reading logs", attrs...)
			return
		}
	}()

	go func() {
		select {
        case <-readDone:
            return
        case <-processDone:
			// if the process channel closes before the read channel, the process may have never opened the file
			// open here to stop blocking call and allow above goroutine to finish
            f, err := os.OpenFile(logPipePath, os.O_WRONLY, 0600)
			if err == nil {
				f.Close()
			}
			return
        }
	}()

	return processDone, nil
}