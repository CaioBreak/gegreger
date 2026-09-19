package main

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	validateURL = "https://auth.roblox.com/v1/usernames/validate?birthday=2000-01-01&username=%s&context=Signup"

	delayPorUsername       = 4 * time.Second
	proxyRateLimit        = 1000
	requestsEmVooPorProxy = 2
	startupRampPerSec     = 300

	timeout  = 10 * time.Second
	userAgent = "Mozilla/5.0"

	retriesTransient = 1

	usernamesFile = "usernames.txt"
	proxiesFile   = "proxies.txt"
	outputFile    = "available.txt"

	loopForever = true
	skipFound   = true

	webhookEnabled = true
	discordWebhook = "https://discord.com/api/webhooks/1525733217343111238/i6DdiTJyHQbJw02dWg6HHUbqPKiTwzk_TpXOEd4yZGfsyeO6sc0ZxBcu1xf26U7EhIFz"

	autoClaimEnabled = true
	cookieFile       = "cookie.txt"
)

const proxyMinInterval = time.Minute / proxyRateLimit

var tlsSessionCache = tls.NewLRUClientSessionCache(4096)

var (
	checkedCount   atomic.Int64
	availableCount atomic.Int64
	takenCount     atomic.Int64
	blockedCount   atomic.Int64
	errorCount     atomic.Int64
	startTime      time.Time
	foundAvailable sync.Map
	activeProxies  atomic.Int64
	latSumMs       atomic.Int64
	latCount       atomic.Int64
)

var (
	cpmWin rateWindow
	epmWin rateWindow
)

type rateWindow struct {
	mu      sync.Mutex
	buckets [60]int64
	lastSec int64
}

func (r *rateWindow) advance(now int64) {
	if now == r.lastSec {
		return
	}
	if now-r.lastSec >= 60 {
		r.buckets = [60]int64{}
	} else {
		for s := r.lastSec + 1; s <= now; s++ {
			r.buckets[s%60] = 0
		}
	}
	r.lastSec = now
}

func (r *rateWindow) add() {
	now := time.Now().Unix()
	r.mu.Lock()
	r.advance(now)
	r.buckets[now%60]++
	r.mu.Unlock()
}

func (r *rateWindow) perMinute() int64 {
	now := time.Now().Unix()
	r.mu.Lock()
	r.advance(now)
	var sum int64
	for _, b := range r.buckets {
		sum += b
	}
	r.mu.Unlock()
	return sum
}

func markValid() { checkedCount.Add(1); cpmWin.add() }
func markError() { errorCount.Add(1); epmWin.add() }

type validateResp struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ---- log compacto (uma linha por username) ----

const useColor = true

const (
	cReset  = "\x1b[0m"
	cGreen  = "\x1b[32m"
	cRed    = "\x1b[31m"
	cCyan   = "\x1b[36m"
	cYellow = "\x1b[33m"
	cGray   = "\x1b[90m"
)

func colorize(c, s string) string {
	if !useColor {
		return s
	}
	return c + s + cReset
}

func logLine(color, tag, username string, detail string) {
	tagStr := colorize(color, fmt.Sprintf("%-5s", tag))
	rate := colorize(cGray, fmt.Sprintf("cpm:%d epm:%d", cpmWin.perMinute(), epmWin.perMinute()))
	if detail == "" {
		fmt.Printf("%s %-16s %s\n", tagStr, username, rate)
		return
	}
	fmt.Printf("%s %-16s %-22s %s\n", tagStr, username, detail, rate)
}

func trimErr(err error) string {
	s := err.Error()
	if i := strings.LastIndex(s, ": "); i >= 0 && i+2 < len(s) {
		s = s[i+2:]
	}
	return s
}

// ---- notificacao Discord ----

var webhookQueue = make(chan string, 256)

var webhookClient = &http.Client{Timeout: 15 * time.Second}

func webhookWorker() {
	for username := range webhookQueue {
		postWebhook(username)
	}
}

func postWebhook(username string) {
	payload := map[string]any{
		"content":          fmt.Sprintf("@everyone\n**Username disponivel:** `%s`", username),
		"allowed_mentions": map[string]any{"parse": []string{"everyone"}},
	}
	body, _ := json.Marshal(payload)

	for attempt := 0; attempt < 5; attempt++ {
		resp, err := webhookClient.Post(discordWebhook, "application/json", bytes.NewReader(body))
		if err != nil {
			logLine(cGray, "HOOK", username, "erro webhook: "+trimErr(err))
			time.Sleep(2 * time.Second)
			continue
		}

		switch {
		case resp.StatusCode == 200 || resp.StatusCode == 204:
			resp.Body.Close()
			logLine(cGreen, "HOOK", username, "enviado ao Discord")
			return
		case resp.StatusCode == 429:
			var ra struct {
				RetryAfter float64 `json:"retry_after"`
			}
			json.NewDecoder(resp.Body).Decode(&ra)
			resp.Body.Close()
			wait := time.Duration(ra.RetryAfter*1000) * time.Millisecond
			if wait <= 0 {
				wait = 2 * time.Second
			}
			time.Sleep(wait)
		default:
			resp.Body.Close()
			logLine(cGray, "HOOK", username, fmt.Sprintf("webhook status %d", resp.StatusCode))
			return
		}
	}
}

// ---- auto-claim de username ----

var (
	claimMu        sync.Mutex
	claimStarted   bool
	alreadyClaimed bool
)

func notifyClaim(username, msg string) {
	if !webhookEnabled {
		return
	}
	payload, _ := json.Marshal(map[string]any{
		"content":          fmt.Sprintf("@everyone\n%s", msg),
		"allowed_mentions": map[string]any{"parse": []string{"everyone"}},
	})
	for attempt := 0; attempt < 3; attempt++ {
		resp, err := webhookClient.Post(discordWebhook, "application/json", bytes.NewReader(payload))
		if err != nil {
			time.Sleep(2 * time.Second)
			continue
		}
		if resp.StatusCode == 429 {
			var ra struct {
				RetryAfter float64 `json:"retry_after"`
			}
			json.NewDecoder(resp.Body).Decode(&ra)
			resp.Body.Close()
			wait := time.Duration(ra.RetryAfter*1000) * time.Millisecond
			if wait <= 0 {
				wait = 2 * time.Second
			}
			time.Sleep(wait)
			continue
		}
		resp.Body.Close()
		return
	}
}

func releaseClaim() {
	claimMu.Lock()
	if !alreadyClaimed {
		claimStarted = false
	}
	claimMu.Unlock()
}

func attemptClaim(username string) {
	claimMu.Lock()
	if claimStarted {
		claimMu.Unlock()
		logLine(cGray, "CLAIM", username, "troca ja em andamento/feita")
		return
	}
	claimStarted = true
	claimMu.Unlock()

	logLine(cYellow, "CLAIM", username, "iniciando auto-claim...")

	cookie, err := os.ReadFile(cookieFile)
	if err != nil {
		logLine(cRed, "CLAIM", username, "erro ao ler cookie.txt: "+err.Error())
		notifyClaim(username, fmt.Sprintf("**Falha ao clamar** `%s`: erro ao ler cookie — %s", username, err.Error()))
		releaseClaim()
		return
	}

	cookieStr := strings.TrimSpace(string(cookie))
	if cookieStr == "" {
		logLine(cRed, "CLAIM", username, "cookie.txt vazio")
		notifyClaim(username, fmt.Sprintf("**Falha ao clamar** `%s`: cookie.txt vazio", username))
		releaseClaim()
		return
	}

	start := time.Now()
	cmd := exec.Command("node", "chef_solver_wrapper.js", username, cookieStr)
	output, err := cmd.CombinedOutput()
	elapsed := time.Since(start)

	outStr := strings.TrimSpace(string(output))

	if err != nil {
		logLine(cRed, "CLAIM", username, fmt.Sprintf("falhou apos %v: %s", elapsed, trimErr(err)))
		if outStr != "" {
			lines := strings.Split(outStr, "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if line != "" {
					logLine(cGray, "CHEF", username, line)
				}
			}
		}
		notifyClaim(username, fmt.Sprintf("**Falha ao clamar** `%s`\n**Motivo:** %s (levou %v)", username, trimErr(err), elapsed))
		releaseClaim()
		return
	}

	if strings.Contains(outStr, "[WRAPPER] SUCCESS") {
		claimMu.Lock()
		alreadyClaimed = true
		claimMu.Unlock()
		logLine(cGreen, "CLAIM", username, fmt.Sprintf("USERNAME CLAMADO COM SUCESSO! (levou %v)", elapsed))
		notifyClaim(username, fmt.Sprintf("**Username clamado com sucesso:** `%s`! (levou %v)", username, elapsed))
	} else {
		logLine(cRed, "CLAIM", username, fmt.Sprintf("solver nao retornou sucesso (levou %v)", elapsed))
		if outStr != "" {
			lines := strings.Split(outStr, "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if line != "" {
					logLine(cGray, "CHEF", username, line)
				}
			}
		}
		notifyClaim(username, fmt.Sprintf("**Falha ao clamar** `%s`\n**Motivo:** solver nao retornou sucesso (levou %v)", username, elapsed))
		releaseClaim()
	}
}

// ---- proxy ----

func normalizeProxy(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return raw
	}
	scheme := "http"
	rest := raw
	if before, after, found := strings.Cut(raw, "://"); found {
		scheme = before
		rest = after
	}
	if strings.Contains(rest, "@") {
		return scheme + "://" + rest
	}
	parts := strings.Split(rest, ":")
	if len(parts) == 4 {
		host, port, user, pass := parts[0], parts[1], parts[2], parts[3]
		u := url.URL{Scheme: scheme, User: url.UserPassword(user, pass), Host: host + ":" + port}
		return u.String()
	}
	return scheme + "://" + rest
}

func newProxyClient(proxy string) (*http.Client, error) {
	proxyURL, err := url.Parse(normalizeProxy(proxy))
	if err != nil {
		return nil, err
	}
	transport := &http.Transport{
		Proxy: http.ProxyURL(proxyURL),
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 60 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2: false,
		TLSNextProto:      map[string]func(string, *tls.Conn) http.RoundTripper{},
		TLSClientConfig: &tls.Config{
			MinVersion:         tls.VersionTLS13,
			ClientSessionCache: tlsSessionCache,
		},
		MaxIdleConns:          requestsEmVooPorProxy,
		MaxIdleConnsPerHost:   requestsEmVooPorProxy,
		MaxConnsPerHost:       requestsEmVooPorProxy,
		IdleConnTimeout:       5 * time.Minute,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		DisableKeepAlives:     false,
	}
	return &http.Client{Transport: transport, Timeout: timeout}, nil
}

// ---- checagem ----

func checkOnce(client *http.Client, username string) (int, int, error) {
	reqURL := fmt.Sprintf(validateURL, url.QueryEscape(username))
	req, err := http.NewRequest(http.MethodGet, reqURL, nil)
	if err != nil {
		return 0, 0, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Close = false

	resp, err := client.Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return resp.StatusCode, 0, err
	}
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, 0, nil
	}
	var vr validateResp
	if err := json.Unmarshal(body, &vr); err != nil {
		return resp.StatusCode, 0, fmt.Errorf("json invalido: %w", err)
	}
	return resp.StatusCode, vr.Code, nil
}

func checkUsername(client *http.Client, proxy, username string, availableOut chan<- string) {
	for attempt := 0; ; attempt++ {
		reqStart := time.Now()
		status, code, err := checkOnce(client, username)
		latSumMs.Add(time.Since(reqStart).Milliseconds())
		latCount.Add(1)

		if err != nil {
			markError()
			if attempt < retriesTransient {
				logLine(cGray, "retry", username, trimErr(err))
				time.Sleep(200 * time.Millisecond)
				continue
			}
			logLine(cGray, "ERRO", username, trimErr(err)+" "+short(proxy))
			return
		}
		if status != http.StatusOK {
			markError()
			if attempt < retriesTransient {
				logLine(cGray, "retry", username, fmt.Sprintf("status %d", status))
				time.Sleep(200 * time.Millisecond)
				continue
			}
			logLine(cGray, "HTTP", username, fmt.Sprintf("status %d %s", status, short(proxy)))
			return
		}

		markValid()
		switch code {
		case 0:
			if _, dup := foundAvailable.LoadOrStore(username, true); !dup {
				availableCount.Add(1)
				logLine(cGreen, "LIVRE", username, "*** NOVO ***")
				availableOut <- username
				if autoClaimEnabled {
					go attemptClaim(username)
				}
				if webhookEnabled {
					select {
					case webhookQueue <- username:
					default:
						logLine(cGray, "HOOK", username, "fila da webhook cheia, pulado")
					}
				}
			} else {
				logLine(cGreen, "LIVRE", username, "")
			}
		case 1:
			takenCount.Add(1)
			logLine(cRed, "USADO", username, "")
		default:
			blockedCount.Add(1)
			logLine(cCyan, "BLOCK", username, "")
		}
		return
	}
}

func short(proxy string) string {
	if u, err := url.Parse(normalizeProxy(proxy)); err == nil && u.Host != "" {
		return u.Host
	}
	if i := strings.LastIndex(proxy, "@"); i >= 0 {
		return proxy[i+1:]
	}
	return proxy
}

func runProxy(proxy string, jobs <-chan string, reschedule func(string), availableOut chan<- string, wg *sync.WaitGroup) {
	defer wg.Done()

	client, err := newProxyClient(proxy)
	if err != nil {
		fmt.Printf("[PROXY INVALIDA] %s -> %v\n", short(proxy), err)
		return
	}
	defer client.CloseIdleConnections()
	activeProxies.Add(1)

	ticker := time.NewTicker(proxyMinInterval)
	defer ticker.Stop()

	var sub sync.WaitGroup
	for i := 0; i < requestsEmVooPorProxy; i++ {
		sub.Add(1)
		go func() {
			defer sub.Done()
			for username := range jobs {
				<-ticker.C
				checkUsername(client, proxy, username, availableOut)
				reschedule(username)
			}
		}()
	}
	sub.Wait()
}

func loadLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line != "" {
			out = append(out, line)
		}
	}
	return out, sc.Err()
}

func main() {
	usernames, err := loadLines(usernamesFile)
	if err != nil {
		fmt.Println("[FATAL] nao abriu", usernamesFile, ":", err)
		os.Exit(1)
	}
	proxies, err := loadLines(proxiesFile)
	if err != nil {
		fmt.Println("[FATAL] nao abriu", proxiesFile, ":", err)
		os.Exit(1)
	}
	if len(usernames) == 0 || len(proxies) == 0 {
		fmt.Println("[FATAL] usernames.txt ou proxies.txt vazio")
		os.Exit(1)
	}

	modo := "passada unica"
	if loopForever {
		modo = "loop infinito"
	}
	fmt.Printf("Usernames: %d | Proxies: %d | Delay/username: %v | Limite/proxy: %d/min | Modo: %s\n",
		len(usernames), len(proxies), delayPorUsername, proxyRateLimit, modo)
	fmt.Printf("Legenda: %s disponivel  %s em uso  %s bloqueado  %s rate-limit  %s erro de conexao\n\n",
		colorize(cGreen, "LIVRE"), colorize(cRed, "USADO"), colorize(cCyan, "BLOCK"),
		colorize(cYellow, "429"), colorize(cGray, "ERRO"))

	outF, err := os.Create(outputFile)
	if err != nil {
		fmt.Println("[FATAL] nao criou", outputFile, ":", err)
		os.Exit(1)
	}
	defer outF.Close()
	w := bufio.NewWriter(outF)

	availableOut := make(chan string, 256)
	var writerWG sync.WaitGroup
	writerWG.Add(1)
	go func() {
		defer writerWG.Done()
		for name := range availableOut {
			w.WriteString(name + "\n")
			w.Flush()
		}
	}()

	if webhookEnabled {
		go webhookWorker()
	}

	jobs := make(chan string, len(usernames)+1)
	var remaining atomic.Int64
	remaining.Store(int64(len(usernames)))

	reschedule := func(u string) {
		if !loopForever {
			if remaining.Add(-1) == 0 {
				close(jobs)
			}
			return
		}
		if skipFound {
			if _, ok := foundAvailable.Load(u); ok {
				return
			}
		}
		time.AfterFunc(delayPorUsername, func() { jobs <- u })
	}

	for _, u := range usernames {
		jobs <- u
	}

	startTime = time.Now()
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				printProgress(len(usernames))
			}
		}
	}()

	launchDelay := time.Second / time.Duration(startupRampPerSec)
	rampSecs := len(proxies) / startupRampPerSec
	fmt.Printf("Subindo %d proxies a %d/s (~%ds pra todas ficarem online)...\n\n", len(proxies), startupRampPerSec, rampSecs)
	var wg sync.WaitGroup
	for _, p := range proxies {
		wg.Add(1)
		go runProxy(p, jobs, reschedule, availableOut, &wg)
		time.Sleep(launchDelay)
	}
	wg.Wait()

	close(availableOut)
	writerWG.Wait()
	close(done)

	fmt.Println("\n==================== FINALIZADO ====================")
	printProgress(len(usernames))
	fmt.Printf("Disponiveis salvos em %s\n", outputFile)
}

func printProgress(total int) {
	checked := checkedCount.Load()
	errs := errorCount.Load()
	processed := checked + errs
	cycle := processed/int64(total) + 1
	inCycle := processed % int64(total)
	avgLat := int64(0)
	if n := latCount.Load(); n > 0 {
		avgLat = latSumMs.Load() / n
	}
	line := fmt.Sprintf("-- ciclo %d (%d/%d) | CPM %d . EPM %d | ativas %d | lat ~%dms | livre %d . uso %d . bloq %d . erro %d --",
		cycle, inCycle, total, cpmWin.perMinute(), epmWin.perMinute(),
		activeProxies.Load(), avgLat,
		availableCount.Load(), takenCount.Load(), blockedCount.Load(), errs)
	fmt.Println(colorize(cYellow, line))
}
