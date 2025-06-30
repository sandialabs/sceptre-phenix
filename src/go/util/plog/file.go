package plog

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path"
	"path/filepath"
	"phenix/util/plog/lumberjack"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/exp/slices"
	"golang.org/x/exp/slog"
)

var (
	fileLogger = lumberjack.Logger{
		Compress: false,
	}

	loggerOpts = FileHandlerOpts{}

	logCache = Cache{
		Cache:      make(map[int64]CacheEntry),
		FileMap:    make(map[int]CacheFileInfo),
		Fidelity:   time.Minute * 10,
		FirstEntry: time.Now().Truncate(time.Minute * 10).UnixMicro(),
	}

	fixedLogKeys = map[string]struct{}{ "time": struct{}{}, "level": struct{}{}, "type": struct{}{}, "msg": struct{}{}}

	TimestampFormat = "2006-01-02 15:04:05.000"
	cacheLock       sync.Mutex
	cachePath       string
)

type FileHandlerOpts struct {
	MaxSize    int
	MaxAge     int
	MaxBackups int

	Level slog.Level
}

func GetDefaultFileHandlerOpts() FileHandlerOpts {
	return FileHandlerOpts{
		MaxSize:    100,
		MaxAge:     90,
		MaxBackups: 3,
	}
}

func AddFileHandler(fname string, opts FileHandlerOpts) {
	file_path := fname

	//if already exists and is dir make 'phenix.log' in dir
	if stat, err := os.Stat(fname); err == nil {
		if stat.IsDir() {
			file_path = path.Join(fname, "phenix.log")
		}
	}

	fileLogger.Filename = file_path
	cachePath = path.Join(path.Dir(file_path), "lookupCache.json")

	loggerOpts = opts

	fileLogger.MaxAge = opts.MaxAge
	fileLogger.MaxBackups = opts.MaxBackups
	fileLogger.MaxSize = opts.MaxSize

	slogOpts := &slog.HandlerOptions{
		Level: loggerOpts.Level.Level(),
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				timestamp := a.Value.Any().(time.Time)
				a.Value = slog.Int64Value(timestamp.UnixMicro())
			}
			return a
		},
	}
	handler.AddHandler("filelogger", slog.NewJSONHandler(&fileLogger, slogOpts))

	//change to a go thread for main release .. in case has to build
	loadCache()
}

func CloseFile() error {
	return fileLogger.Close()
}

func ChangeMaxLogFileSize(bytes int) {
	fileLogger.MaxSize = bytes
}
func ChangeMaxLogFileBackups(files int) {
	fileLogger.MaxBackups = files
}
func ChangeMaxLogFileAge(days int) {
	fileLogger.MaxAge = days
}

type Cache struct {
	Cache map[int64]CacheEntry `json:"cache"`

	FileMap    map[int]CacheFileInfo `json:"file_map"`
	FileOrder  []int                 `json:"file_order"`
	NextFileID int                   `json:"next_file_id"`

	Fidelity   time.Duration `json:"fidelity"`
	FirstEntry int64         `json:"first_entry"`
	LastEntry  int64         `json:"last_entry"`
}

type CacheEntry struct {
	File               int   `json:"file"`
	BytePos            int   `json:"byte"`
	TimestampUnixMicro int64 `json:"ts"`
}

type CacheFileInfo struct {
	Filename string `json:"filename"`

	FirstTime time.Time `json:"first_time"`
	LastTime  time.Time `json:"last_time"`

	FirstCacheKey int64 `json:"first_cache_key"`
	LastCacheKey  int64 `json:"last_cache_key"`
}

type LogEntry struct {
	Time      int64          `json:"time"`
	Timestamp string         `json:"timestamp,omitempty"`
	Level     string         `json:"level"`
	Message   string         `json:"msg"`
	Type      string         `json:"type"`
}

// unmarshals LogEntry. Appends extra keys to Message
func (log *LogEntry) UnmarshalJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))

	// Expect the start of the object
	t, err := decoder.Token()
	if err != nil {
		return err
	}
	if t != json.Delim('{') {
		return fmt.Errorf("expected start of JSON object")
	}

	msgAttrs := []string{}

	// Read key-value pairs
	for decoder.More() {
		// Read the key
		t, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := t.(string)
		if !ok {
			return fmt.Errorf("expected string key")
		}

		// Read the value
		var value interface{}
		if err := decoder.Decode(&value); err != nil {
			return err
		}

		if key == "time" {
			ts, ok := value.(float64)
			if !ok {
				return fmt.Errorf("could not parse time as float")
			}
			log.Time = int64(ts)
			log.Timestamp = time.UnixMicro(log.Time).Format(TimestampFormat)
		} else if key == "level" {
			level, ok := value.(string)
			if !ok {
				return fmt.Errorf("could not parse level as string")
			}
			log.Level = level
		} else if key == "type" {
			t, ok := value.(string)
			if !ok {
				return fmt.Errorf("could not parse type as string")
			}
			log.Type = t
		} else if key == "msg" {
			msg, ok := value.(string)
			if !ok {
				return fmt.Errorf("could not parse msg as string")
			}
			log.Message = msg
		} else {
			msgAttrs = append(msgAttrs, fmt.Sprintf("%s=%v", key, value))
		}
	}

	log.Message = fmt.Sprintf("%s %s", log.Message, strings.Join(msgAttrs, " "))

	// Expect the end of the object
	t, err = decoder.Token()
	if err != nil {
		return err
	}
	if t != json.Delim('}') {
		return fmt.Errorf("expected end of JSON object")
	}

	return nil
}

func GetLogs(start, end time.Time) ([]LogEntry, error) {
	timerStart := time.Now()

	if ok, err := checkCacheIntegrity(); !ok {
		Error(TypeSecurity, "error with cache integrity", "error", err)
		buildCache()
	}

	cacheLock.Lock()
	defer cacheLock.Unlock()

	startKey := start.Truncate(logCache.Fidelity).UnixMicro()
	if start.UnixMicro() < logCache.FirstEntry {
		Debug(TypeSystem, "Setting start key to the first key entry")
		startKey = logCache.FirstEntry
	} else if start.UnixMicro() > logCache.LastEntry {
		//shouldn't hit
		Debug(TypeSystem, "Setting start key to the last key entry")
		startKey = logCache.LastEntry
	}

	cacheHit, ok := logCache.Cache[startKey]
	if !ok {
		fmt.Println(startKey, logCache.FirstEntry, logCache.LastEntry)
		Error(TypeSecurity, "error getting cache hit", "startKey", startKey,
			"first entry", logCache.FirstEntry, "last entry", logCache.LastEntry,
			"start", start.UnixMicro())
		//this should never happen, especially after cache rebuild
		//only should happen with 0 log files present
		return nil, fmt.Errorf("error getting cache hit. Cache may be empty or corrupted. Try rebooting")
	}

	currFileIdx := cacheHit.File
	currFilename := logCache.FileMap[currFileIdx].Filename
	currSeek := cacheHit.BytePos

	Debug(TypeSystem, "starting get logs", "startKey", startKey, "cache hit", cacheHit,
		"currFilename", currFilename, "currFileIdx", currFileIdx)

	var result []LogEntry

	for {
		fileRes, done, err := getMatchingLogsOneFile(start, end, currFilename, int64(currSeek))
		if err != nil {
			Error(TypeSystem, "error getting logs from file", "file", currFilename, "start", start, "end",
				end, "error", err, "seek", currSeek)
			return result, fmt.Errorf("error getting logs from file: %w", err)
		}

		Debug(TypeSystem, "got logs from file", "filename", currFilename, "done", done, "error", err)
		result = append(result, fileRes...)

		if done {
			break
		}

		currFileIdx = logCache.getNextFileIdx(currFileIdx)
		if currFileIdx == -1 {
			// Debug(TypeSystem, "Got next file idx as -1", "file order", logCache.FileOrder)
			break
		}

		currFilename = logCache.FileMap[currFileIdx].Filename
		currSeek = 0
	}

	Debug(TypeSystem, "Completed GetLogs request", "time (s)", time.Since(timerStart).Seconds(), "num logs", len(result))

	return result, nil
}

func getMatchingLogsOneFile(start, end time.Time, fname string, seek int64) (result []LogEntry, done bool, err error) {
	Debug(TypeSystem, "reading log file for logs", "start", start, "end", end, "filename", fname, "seek", seek)

	result = make([]LogEntry, 0)
	countProcessed := 0

	file, err := os.Open(fname)
	if err != nil {
		return nil, false, err
	}
	defer file.Close()

	file.Seek(seek, 0)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Bytes()
		countProcessed += 1

		entry := LogEntry{}
		if err := json.Unmarshal(line, &entry); err != nil {
			//skip malformed entries
			Error(TypeSystem, "malformed log entry", "error", err, "line", string(line))
			continue
		}

		if entry.Time > end.Add(time.Minute).UnixMicro() {
			//hit the end, return done
			Debug(TypeSystem, "processed logs from file. found end of range", "count", countProcessed, "result", len(result))
			return result, true, nil
		}

		if entry.Time > start.UnixMicro() && entry.Time < end.UnixMicro() {
			result = append(result, entry)
		}
	}

	//got to end of file without reaching end of search... continue
	Debug(TypeSystem, "processed logs from file", "count", countProcessed, "result", len(result))
	return result, false, nil
}

func (c *Cache) getNextFileIdx(prev int) int {
	was_prev := false
	for _, n := range c.FileOrder {
		if was_prev {
			return n
		}
		if n == prev {
			was_prev = true
		}
	}
	return -1
}

// build from scratch. hopefully doesn't have to be called often
func buildCache() {
	Info(TypeSystem, "rebuilding log lookup cache from scratch")
	start := time.Now()

	newCache := Cache{
		Cache:      make(map[int64]CacheEntry),
		FileMap:    make(map[int]CacheFileInfo),
		Fidelity:   time.Minute * 10,
		FirstEntry: time.Now().Truncate(time.Minute * 10).UnixMicro(),
		LastEntry:  0,
	}

	for _, logFile := range getLogFilesInDirectory() {
		newCache.AddFileToCache(logFile)
	}

	newCache.SetFileOrder()
	cacheLock.Lock()
	logCache = newCache
	cacheLock.Unlock()

	Debug(TypeSystem, "cache rebuilt", "time (s)", time.Since(start).Seconds())
	err := saveCache()
	if err != nil {
		Error("error saving cache", "error", err)
	}
}

func filenameMatchesLogger(fname string) bool {
	//ex: phenix.log is fileLogger.Filename.
	// phenix-old-log.log matches
	// error.log does not match
	baseMatch := filepath.Base(fileLogger.Filename)
	baseMatch = strings.TrimSuffix(baseMatch, filepath.Ext(baseMatch))
	pattern := regexp.MustCompile(fmt.Sprintf("^%s", baseMatch))

	return pattern.MatchString(filepath.Base(fname))
}

func (c *Cache) SetFileOrder() {
	type FileOrder struct {
		FileID int
		Start  time.Time
	}
	var files []FileOrder
	for fileID, info := range c.FileMap {
		files = append(files, FileOrder{FileID: fileID, Start: info.FirstTime})
	}
	slices.SortFunc(files, func(a, b FileOrder) bool {
		return a.Start.Before(b.Start)
	})

	c.FileOrder = make([]int, 0)
	for _, f := range files {
		c.FileOrder = append(c.FileOrder, f.FileID)
	}

}

func (c *Cache) AddFileToCache(fname string) error {
	Debug(TypeSystem, "adding file to cache", "filename", fname)
	f, err := os.Open(fname)
	if err != nil {
		return fmt.Errorf("Error opening file %v: %w", fname, err)
	}
	defer f.Close()

	currFileID := c.NextFileID
	byteCount := 0
	var firstTime int64 = math.MaxInt64
	var lastTime int64 = 0
	var firstCacheKey int64 = math.MaxInt64
	latestEntry := time.Time{}

	for fileID, fileInfo := range c.FileMap {
		if fileInfo.Filename != fname {
			continue
		}
		Debug(TypeSystem, "file already in cache. updating file", "file", fname)

		//if already in cache, get this info
		currFileID = fileID
		byteCount = c.Cache[fileInfo.LastCacheKey].BytePos
		firstTime = fileInfo.FirstTime.UnixMicro()
		lastTime = fileInfo.LastTime.UnixMicro()
		firstCacheKey = fileInfo.FirstCacheKey
		latestEntry = time.UnixMicro(fileInfo.LastCacheKey)

		f.Seek(int64(byteCount), 0) //start at latest bit
		break
	}

	reader := bufio.NewReader(f)

	badLineCount := 0

	//read through entire file line by line
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if err.Error() != "EOF" {
				return fmt.Errorf("Error reading file %v from bufio reader: %w", fname, err)
			}
			break
		}

		//get LogEntry from line
		var entry LogEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			badLineCount += 1
			//following line can clog up logs with misformatting logs. Might be a good idea to have one or two
			//instances. if someone adds a bad logfile to directory we could see a ton of these entries
			if badLineCount < 3 {
				Error(TypeSystem, "error with json unmarshal", "filename", fname, "line", string(line), "error", err, "bad line count", badLineCount)
			}
			if badLineCount == 3 {
				Error(TypeSystem, "error with json unmarshal", "filename", fname, "line", string(line), "error", err, "bad line count", badLineCount)
				Error(TypeSystem, "too many errors with json unmarshal. stopping reports of errors for file", "filename", fname)
			}
			continue //ignore for now...
		}

		//update file data for first and last time
		if entry.Time < firstTime {
			firstTime = entry.Time
		}
		if entry.Time > lastTime {
			lastTime = entry.Time
		}

		entryHit := time.UnixMicro(entry.Time).Truncate(c.Fidelity)

		//if we haven't passed log fidelity line, keep going
		if !entryHit.After(latestEntry) {
			byteCount += len(line)
			continue
		}

		// Info(TypeSystem, "got past checks", "entryHit", entryHit, "latestEntry", latestEntry)
		if latestEntry.IsZero() { //don't want to add every timestamp since epoch
			latestEntry = entryHit
		}

		cacheEntry := CacheEntry{BytePos: byteCount, File: currFileID, TimestampUnixMicro: entry.Time}

		missingEntries := latestEntry.Add(c.Fidelity)
		for entryHit.After(missingEntries) {
			c.Cache[missingEntries.UnixMicro()] = cacheEntry
			missingEntries = missingEntries.Add(c.Fidelity)
		}

		entryKey := entryHit.UnixMicro()

		prevEntry, ok := c.Cache[entryKey]
		if !ok || cacheEntry.TimestampUnixMicro < prevEntry.TimestampUnixMicro {
			//only add to cache if it doesn't exist, or if it exists and the new timestamp is older than old one

			// this an issue with starting a new file. by default, it will add a cache entry for the
			// first timestamp in the cache, overwriting the cache hit for the previous file
			c.Cache[entryKey] = cacheEntry
		}

		if entryHit.UnixMicro() < firstCacheKey {
			firstCacheKey = entryHit.UnixMicro()
		}
		latestEntry = entryHit
		byteCount += len(line)
	}

	fileInfo := CacheFileInfo{
		Filename:      fname,
		FirstTime:     time.UnixMicro(firstTime),
		LastTime:      time.UnixMicro(lastTime),
		FirstCacheKey: firstCacheKey,
		LastCacheKey:  latestEntry.UnixMicro(),
	}

	c.FileMap[currFileID] = fileInfo
	c.NextFileID += 1

	if c.FirstEntry > firstCacheKey {
		c.FirstEntry = firstCacheKey
	}
	if c.LastEntry < latestEntry.UnixMicro() {
		c.LastEntry = latestEntry.UnixMicro()
	}
	return nil
}

func checkCacheIntegrity() (ok bool, err error) {
	start := time.Now()

	cacheLock.Lock()
	defer cacheLock.Unlock()

	//adding missing files, add current file to cache don't
	logCache.AddMissingFiles()
	err = logCache.AddFileToCache(fileLogger.Filename)
	if err != nil {
		return false, fmt.Errorf("error adding file %v to cache: %w", fileLogger.Filename, err)
	}

	if len(logCache.FileMap) != len(logCache.FileOrder) {
		logCache.SetFileOrder()
	}

	//check each file
	for fileID := range logCache.FileMap {
		ok, err := logCache.checkLogFileIntegrity(fileID, logCache.FileMap[fileID].Filename)
		if !ok || err != nil {
			return false, fmt.Errorf("error with log file id %v: %w", fileID, err)
		}
	}

	var cacheKeys []int64
	for key := range logCache.Cache {
		cacheKeys = append(cacheKeys, key)
	}
	slices.Sort(cacheKeys)
	duration := logCache.Fidelity.Microseconds()
	for i := 0; i < len(cacheKeys)-1; i++ {
		curr := cacheKeys[i]
		next := cacheKeys[i+1]

		for j := curr + duration; j < next; j += duration {
			logCache.Cache[j] = logCache.Cache[next]
		}

	}

	saveCacheNoBlock()
	Debug(TypeSystem, "cache integrity check finished", "time (s)", time.Since(start).Seconds())
	return true, nil
}

func (c *Cache) RemoveDeletedFiles() {
	logFiles := getLogFilesInDirectory()

	var fileIDremove []int
	for fileID, fileInfo := range c.FileMap {
		if !slices.Contains(logFiles, fileInfo.Filename) {
			fileIDremove = append(fileIDremove, fileID)
			delete(c.FileMap, fileID)
		}
	}

	for key, cacheHit := range c.Cache {
		slices.Contains(fileIDremove, cacheHit.File)
		delete(c.Cache, key)
	}

}

func (c *Cache) AddMissingFiles() {
	maxIter := 3

	//should take two iterations on a rotation: first to move logfile->rotated file,
	//second to add main logfile
	for i := 0; i < maxIter; i++ {
		missingFiles := c.getLogfilesNotInCache()
		if len(missingFiles) == 0 {
			return
		}

		for _, fname := range missingFiles {
			//if there are any log files that are not in the cache, check if it's a renamed verison
			for fileID, fileInfo := range logCache.FileMap {
				if fileInfo.Filename == fname {
					//only want different filenames
					continue
				}

				ok, _ := c.checkLogFileIntegrity(fileID, fname)
				if ok {
					//these are the same file
					fileInfo.Filename = fname
					logCache.FileMap[fileID] = fileInfo
				}
			}
			//regardless, add file to cache. if updated in map then it just
			//checks the end of the file for new logs. if not, add whole file
			logCache.AddFileToCache(fname)
		}
	}
	c.SetFileOrder()
	saveCacheNoBlock()
}

func getLogFilesInDirectory() []string {
	var logFiles []string

	filepath.WalkDir(path.Dir(cachePath), func(fname string, d fs.DirEntry, err error) error {
		if d.IsDir() {
			return nil
		}
		if path.Ext(fname) != ".log" {
			return nil
		}
		if !filenameMatchesLogger(fname) {
			return nil
		}
		if _, err := os.Stat(fname); err != nil {
			//file doesn't exist / can't be opened??
			return nil
		}
		logFiles = append(logFiles, fname)
		return nil
	})

	return logFiles
}

func (c Cache) getLogfilesNotInCache() []string {
	logFiles := getLogFilesInDirectory()

	existsInCache := make(map[string]bool)
	for _, file := range logFiles {
		existsInCache[file] = false
	}

	for _, value := range c.FileMap {
		existsInCache[value.Filename] = true
	}

	var missingFiles []string

	for file, exists := range existsInCache {
		if !exists {
			missingFiles = append(missingFiles, file)
		}
	}
	return missingFiles
}

func (c Cache) checkLogFileIntegrity(fileID int, filename string) (ok bool, err error) {
	file, err := os.Open(filename)
	if err != nil {
		return false, fmt.Errorf("error opening file %v: %w", filename, err)
	}
	defer file.Close()

	fileInfo := c.FileMap[fileID]
	cacheHit, ok := logCache.Cache[fileInfo.LastCacheKey]
	if !ok {
		return false, fmt.Errorf("cachekey %v does not exist in cache", fileInfo.LastCacheKey)
	}

	file.Seek(int64(cacheHit.BytePos), 0)
	reader := bufio.NewReader(file)
	line, err := reader.ReadBytes('\n')

	if err != nil {
		return false, fmt.Errorf("error reading line: %w", err)
	}

	var log LogEntry
	if err := json.Unmarshal(line, &log); err != nil {
		return false, fmt.Errorf("error with json unmarhsal: %w", err)
	}

	if log.Time != cacheHit.TimestampUnixMicro {
		return false, fmt.Errorf("Timestamps for cache do not match file. file=%v cache=%v", log.Time, cacheHit.TimestampUnixMicro)
	}
	return true, nil
}

func loadCache() {
	if _, err := os.Stat(cachePath); err != nil {
		Debug(TypeSystem, "Problem stat'ing cache path. rebuilding cache...")
		buildCache()
		return
	}

	data, err := os.ReadFile(cachePath)
	if err != nil {
		Debug(TypeSystem, "Problem reading cache path file. rebuilding cache...")
		buildCache()
		return
	}

	var newCache Cache
	err = json.Unmarshal(data, &newCache)
	if err != nil {
		Debug(TypeSystem, "Problem unmarshalling cache json file. Rebuilding...", "error", err)
		buildCache()
		return
	}

	cacheLock.Lock()
	logCache = newCache
	cacheLock.Unlock()

	ok, err := checkCacheIntegrity()
	if err != nil || !ok {
		Info(TypeSystem, "Cache integrity not set during load. rebuilding")
		buildCache()
	}

	Debug(TypeSystem, "loaded cache for logger", "integrity", ok)
}

func saveCache() error {
	cacheLock.Lock()
	defer cacheLock.Unlock()

	return saveCacheNoBlock()
}

func saveCacheNoBlock() error {

	bytes, err := json.Marshal(logCache)
	if err != nil {
		return err
	}

	file, err := os.Create(cachePath)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = file.Write(bytes)
	if err != nil {
		return err
	}

	return nil
}
