package main

import (
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/apache/iotdb-client-go/client"
)

//go:embed web/*.html web/favicon.svg web/favicon.ico
var webFiles embed.FS

type connectionRequest struct {
	Host     string `json:"host"`
	Port     string `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"`
}

type savedConnection struct {
	Name     string `json:"name"`
	Host     string `json:"host"`
	Port     string `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"`
}

type appConfig struct {
	Connections  []savedConnection `json:"connections"`
	SavedQueries []savedSQLQuery   `json:"savedQueries"`
}

type savedSQLQuery struct {
	Name           string `json:"name"`
	SQL            string `json:"sql"`
	ConnectionName string `json:"connectionName"`
	ConnectionHost string `json:"connectionHost"`
	ConnectionPort string `json:"connectionPort"`
	ConnectionUser string `json:"connectionUser"`
}

type sqlRequest struct {
	SQL   string `json:"sql"`
	Limit int    `json:"limit"`
}

type ttlRequest struct {
	Database string `json:"database"`
	Cutoff   int64  `json:"cutoff"`
}

type deviceCountJob struct {
	Running bool
	Count   string
	Error   string
}

type queryResult struct {
	Columns []string   `json:"columns"`
	Types   []string   `json:"types"`
	Rows    [][]string `json:"rows"`
	Message string     `json:"message,omitempty"`
}

type apiError struct {
	Error string `json:"error"`
}

type server struct {
	mu          sync.Mutex
	session     *client.Session
	host        string
	port        string
	user        string
	connected   bool
	configMu    sync.RWMutex
	config      appConfig
	configPath  string
	countMu     sync.Mutex
	countJobs   map[string]*deviceCountJob
	resourceMu  sync.Mutex
	lastCPUTime uint64
	lastCPUAt   time.Time
}

func main() {
	addr := flag.String("addr", "127.0.0.1:52014", "Web listen address")
	openBrowser := flag.Bool("open-browser", true, "Open local page after startup")
	flag.Parse()

	webRoot, err := fs.Sub(webFiles, "web")
	if err != nil {
		log.Fatalf("load embedded web files: %v", err)
	}
	configPath := applicationConfigPath()
	s := &server{config: loadAppConfig(configPath), configPath: configPath, countJobs: make(map[string]*deviceCountJob)}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", s.handleHealth)
	mux.HandleFunc("/api/connect", s.handleConnect)
	mux.HandleFunc("/api/disconnect", s.handleDisconnect)
	mux.HandleFunc("/api/sql", s.handleSQL)
	mux.HandleFunc("/api/browse", s.handleBrowse)
	mux.HandleFunc("/api/settings", s.handleSettings)
	mux.HandleFunc("/api/ttl", s.handleTTL)
	mux.HandleFunc("/api/device-count", s.handleDeviceCount)
	mux.HandleFunc("/api/resources", s.handleResources)
	mux.Handle("/", http.FileServer(http.FS(webRoot)))

	httpServer := &http.Server{Addr: *addr, Handler: withSecurityHeaders(mux), ReadHeaderTimeout: 10 * time.Second}
	url := "http://" + *addr
	log.Printf("Mini Apache IoTDB Manager for Windows 已启动。")
	log.Printf("请在浏览器访问：%s", url)
	log.Printf("关闭此终端窗口将停止服务。")
	if *openBrowser {
		openURL(url)
	}
	log.Fatal(httpServer.ListenAndServe())
}

func withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func openURL(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}

func applicationConfigPath() string {
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		return filepath.Join(home, ".mini-manage", "iotdb-manage.json")
	}
	return "iotdb-manage.json"
}

func loadAppConfig(path string) appConfig {
	config := appConfig{Connections: []savedConnection{}, SavedQueries: []savedSQLQuery{}}
	data, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(data, &config) != nil {
		return config
	}
	if config.Connections == nil {
		config.Connections = []savedConnection{}
	}
	if config.SavedQueries == nil {
		config.SavedQueries = []savedSQLQuery{}
	}
	if validateAppConfig(config) != nil {
		return appConfig{Connections: []savedConnection{}, SavedQueries: []savedSQLQuery{}}
	}
	return config
}

func saveAppConfig(path string, config appConfig) error {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0600)
}

func validConfigName(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && len([]rune(value)) <= 64 && !strings.ContainsAny(value, "\r\n\x00")
}

func validateAppConfig(config appConfig) error {
	if len(config.Connections) > 50 || len(config.SavedQueries) > 100 {
		return errors.New("保存项目数量超过限制")
	}
	seen := map[string]bool{}
	for _, item := range config.Connections {
		key := strings.ToLower(strings.TrimSpace(item.Name))
		if !validConfigName(item.Name) || seen[key] || strings.TrimSpace(item.Host) == "" || strings.TrimSpace(item.Port) == "" || strings.TrimSpace(item.User) == "" {
			return errors.New("保存的连接信息无效或名称重复")
		}
		seen[key] = true
	}
	seenQueries := map[string]bool{}
	for _, item := range config.SavedQueries {
		key := strings.ToLower(strings.TrimSpace(item.ConnectionHost) + "\x00" + strings.TrimSpace(item.ConnectionPort) + "\x00" + strings.TrimSpace(item.ConnectionUser) + "\x00" + strings.TrimSpace(item.Name))
		if !validConfigName(item.Name) || seenQueries[key] || strings.TrimSpace(item.SQL) == "" || len(item.SQL) > 200000 {
			return errors.New("保存的 SQL 无效或名称重复")
		}
		seenQueries[key] = true
	}
	return nil
}

func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"connected": s.connected, "host": s.host, "port": s.port, "user": s.user})
}

type winFiletime struct{ LowDateTime, HighDateTime uint32 }

type processMemoryCountersEx struct {
	CB                         uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
	PrivateUsage               uintptr
}

type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

var (
	kernel32DLL              = syscall.NewLazyDLL("kernel32.dll")
	getProcessTimesProc      = kernel32DLL.NewProc("GetProcessTimes")
	globalMemoryStatusExProc = kernel32DLL.NewProc("GlobalMemoryStatusEx")
	psapiDLL                 = syscall.NewLazyDLL("psapi.dll")
	getProcessMemoryInfoProc = psapiDLL.NewProc("GetProcessMemoryInfo")
)

func processCPUTime() (uint64, error) {
	var creation, exit, kernel, user winFiletime
	result, _, callErr := getProcessTimesProc.Call(^uintptr(0), uintptr(unsafe.Pointer(&creation)), uintptr(unsafe.Pointer(&exit)), uintptr(unsafe.Pointer(&kernel)), uintptr(unsafe.Pointer(&user)))
	if result == 0 {
		return 0, callErr
	}
	toUint64 := func(value winFiletime) uint64 { return uint64(value.HighDateTime)<<32 | uint64(value.LowDateTime) }
	return toUint64(kernel) + toUint64(user), nil
}

func processMemoryUsage() (uint64, uint64, error) {
	counters := processMemoryCountersEx{CB: uint32(unsafe.Sizeof(processMemoryCountersEx{}))}
	result, _, callErr := getProcessMemoryInfoProc.Call(^uintptr(0), uintptr(unsafe.Pointer(&counters)), uintptr(counters.CB))
	if result == 0 {
		return 0, 0, callErr
	}
	memory := memoryStatusEx{Length: uint32(unsafe.Sizeof(memoryStatusEx{}))}
	result, _, callErr = globalMemoryStatusExProc.Call(uintptr(unsafe.Pointer(&memory)))
	if result == 0 {
		return 0, 0, callErr
	}
	return uint64(counters.WorkingSetSize), memory.TotalPhys, nil
}

func (s *server) handleResources(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}
	now := time.Now()
	cpuTime, cpuErr := processCPUTime()
	workingSet, totalMemory, memoryErr := processMemoryUsage()
	var cpuPercent float64
	s.resourceMu.Lock()
	if cpuErr == nil && !s.lastCPUAt.IsZero() {
		elapsed := now.Sub(s.lastCPUAt).Seconds()
		if elapsed > 0 {
			cpuPercent = float64(cpuTime-s.lastCPUTime) / 10000000 / elapsed / float64(runtime.NumCPU()) * 100
			if cpuPercent < 0 {
				cpuPercent = 0
			}
		}
	}
	if cpuErr == nil {
		s.lastCPUTime, s.lastCPUAt = cpuTime, now
	}
	s.resourceMu.Unlock()
	var memoryPercent float64
	if memoryErr == nil && totalMemory > 0 {
		memoryPercent = float64(workingSet) / float64(totalMemory) * 100
	}
	writeJSON(w, http.StatusOK, map[string]any{"backendCPUPercent": cpuPercent, "backendMemoryPercent": memoryPercent, "backendMemoryMB": float64(workingSet) / (1024 * 1024)})
}

func (s *server) handleConnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w)
		return
	}
	var req connectionRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	req.Host = strings.TrimSpace(req.Host)
	req.Port = strings.TrimSpace(req.Port)
	req.User = strings.TrimSpace(req.User)
	if req.Host == "" {
		req.Host = "127.0.0.1"
	}
	if req.Port == "" {
		req.Port = "6667"
	}
	if req.User == "" {
		req.User = "root"
	}

	session := client.NewSession(&client.Config{Host: req.Host, Port: req.Port, UserName: req.User, Password: req.Password, FetchSize: 500})
	if err := session.Open(false, 5000); err != nil {
		writeError(w, http.StatusBadGateway, fmt.Errorf("连接 IoTDB 失败：%w", err))
		return
	}

	s.mu.Lock()
	if s.session != nil {
		_ = s.session.Close()
	}
	s.session, s.host, s.port, s.user, s.connected = &session, req.Host, req.Port, req.User, true
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"message": "已连接到 Apache IoTDB", "host": req.Host, "port": req.Port})
}

func (s *server) handleDisconnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.session != nil {
		_ = s.session.Close()
	}
	s.session, s.connected = nil, false
	writeJSON(w, http.StatusOK, map[string]string{"message": "已断开连接"})
}

func (s *server) handleSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		s.configMu.RLock()
		config := s.config
		s.configMu.RUnlock()
		writeJSON(w, http.StatusOK, config)
		return
	}
	if r.Method != http.MethodPut {
		writeMethodNotAllowed(w)
		return
	}
	var config appConfig
	if err := decodeJSON(r, &config); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.configMu.Lock()
	if config.Connections == nil {
		config.Connections = s.config.Connections
	}
	if config.SavedQueries == nil {
		config.SavedQueries = s.config.SavedQueries
	}
	if err := validateAppConfig(config); err != nil {
		s.configMu.Unlock()
		writeError(w, http.StatusBadRequest, err)
		return
	}
	err := saveAppConfig(s.configPath, config)
	if err == nil {
		s.config = config
	}
	s.configMu.Unlock()
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("无法保存连接信息：%w", err))
		return
	}
	writeJSON(w, http.StatusOK, config)
}

func (s *server) handleTTL(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w)
		return
	}
	var req ttlRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	req.Database = strings.TrimSpace(req.Database)
	if !validIoTDBPath(req.Database) {
		writeError(w, http.StatusBadRequest, errors.New("数据库路径无效"))
		return
	}
	now := time.Now().UnixMilli()
	if req.Cutoff <= 0 || req.Cutoff > now {
		writeError(w, http.StatusBadRequest, errors.New("过期时间必须早于当前时间"))
		return
	}
	ttl := now - req.Cutoff
	if _, err := s.execute(fmt.Sprintf("SET TTL TO %s %d", req.Database, ttl), 1); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "已设置 TTL，指定时间之前的数据将立即过期"})
}

func validIoTDBPath(path string) bool {
	return strings.HasPrefix(path, "root.") && !strings.ContainsAny(path, " \t\r\n;`'\"")
}

func (s *server) handleDeviceCount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}
	device := strings.TrimSpace(r.URL.Query().Get("device"))
	if !validIoTDBPath(device) {
		writeError(w, http.StatusBadRequest, errors.New("设备路径无效"))
		return
	}
	refresh := r.URL.Query().Get("refresh") == "1"
	s.countMu.Lock()
	job := s.countJobs[device]
	if job == nil || refresh {
		job = &deviceCountJob{Running: true}
		s.countJobs[device] = job
		go s.calculateDeviceCount(device, job)
	}
	response := map[string]any{"running": job.Running, "count": job.Count, "error": job.Error}
	s.countMu.Unlock()
	writeJSON(w, http.StatusOK, response)
}

func (s *server) calculateDeviceCount(device string, job *deviceCountJob) {
	result, err := s.execute(fmt.Sprintf("SELECT COUNT_TIME(*) FROM %s", device), 1)
	count := ""
	if err == nil {
		for _, row := range result.Rows {
			for _, value := range row {
				if _, parseErr := strconv.ParseInt(value, 10, 64); parseErr == nil {
					count = value
					break
				}
			}
			if count != "" {
				break
			}
		}
		if count == "" {
			err = errors.New("未返回可识别的记录总数")
		}
	}
	s.countMu.Lock()
	job.Running = false
	job.Count = count
	if err != nil {
		job.Error = err.Error()
	}
	s.countMu.Unlock()
}

func (s *server) handleSQL(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w)
		return
	}
	var req sqlRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	req.SQL = strings.TrimSpace(req.SQL)
	if req.SQL == "" {
		writeError(w, http.StatusBadRequest, errors.New("请输入 SQL"))
		return
	}
	if len(req.SQL) > 200000 {
		writeError(w, http.StatusBadRequest, errors.New("SQL 长度不能超过 200000 个字符"))
		return
	}
	result, err := s.execute(req.SQL, req.Limit)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *server) handleBrowse(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}
	databases, dbErr := s.execute("SHOW DATABASES", 300)
	devices, devErr := s.execute("SHOW DEVICES", 300)
	if dbErr != nil && devErr != nil {
		writeError(w, http.StatusBadRequest, dbErr)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"databases": databases.Rows, "databaseColumns": databases.Columns,
		"devices": devices.Rows, "deviceColumns": devices.Columns,
		"warnings": joinErrors(dbErr, devErr),
	})
}

func (s *server) execute(sql string, limit int) (queryResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.connected || s.session == nil {
		return queryResult{}, errors.New("尚未连接 IoTDB")
	}
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	if !isQuery(sql) {
		if err := s.session.ExecuteNonQueryStatement(sql); err != nil {
			return queryResult{}, err
		}
		return queryResult{Message: "语句执行成功"}, nil
	}
	timeout := int64(60000)
	ds, err := s.session.ExecuteQueryStatement(sql, &timeout)
	if err != nil {
		return queryResult{}, err
	}
	defer ds.Close()
	result := queryResult{Columns: ds.GetColumnNames(), Types: ds.GetColumnTypes(), Rows: make([][]string, 0)}
	for {
		next, err := ds.Next()
		if err != nil {
			return queryResult{}, err
		}
		if !next {
			break
		}
		row := make([]string, len(result.Columns))
		for i, column := range result.Columns {
			// The IoTDB Go SDK uses one-based column indexes.
			null, err := ds.IsNullByIndex(int32(i + 1))
			if err != nil {
				return queryResult{}, err
			}
			if null {
				row[i] = "NULL"
				continue
			}
			value, err := ds.GetString(column)
			if err != nil {
				return queryResult{}, err
			}
			row[i] = value
		}
		result.Rows = append(result.Rows, row)
		if len(result.Rows) >= limit {
			result.Message = fmt.Sprintf("为保护客户端，仅显示前 %d 行。可在 SQL 中添加 LIMIT 获取更多数据。", limit)
			break
		}
	}
	if result.Message == "" {
		result.Message = fmt.Sprintf("查询完成，共返回 %d 行", len(result.Rows))
	}
	return result, nil
}

func isQuery(sql string) bool {
	normalized := strings.TrimLeft(strings.ToLower(sql), " \t\r\n(")
	for _, keyword := range []string{"select", "show", "describe", "desc", "explain", "with", "count"} {
		if strings.HasPrefix(normalized, keyword) {
			return true
		}
	}
	return false
}

func decodeJSON(r *http.Request, dest any) error {
	defer r.Body.Close()
	decoder := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 512<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dest); err != nil {
		return fmt.Errorf("请求数据无效：%w", err)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, apiError{Error: err.Error()})
}
func writeMethodNotAllowed(w http.ResponseWriter) {
	writeError(w, http.StatusMethodNotAllowed, errors.New("不支持此请求方法"))
}
func joinErrors(errs ...error) string {
	parts := make([]string, 0)
	for _, err := range errs {
		if err != nil {
			parts = append(parts, err.Error())
		}
	}
	return strings.Join(parts, "；")
}
