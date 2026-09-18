// checker: varre todos os usernames de usernames.txt usando as proxies de
// proxies.txt. Cada username so e re-checado a cada delayPorUsername (por qualquer
// proxy); cada proxy manda no maximo proxyRateLimit/min. Checa
// disponibilidade pelo endpoint /v1/usernames/validate. Os nomes disponiveis
// (code 0) sao gravados em available.txt conforme vao sendo achados.
//
// Rodar (a partir da pasta do projeto):
//
//	go run .
//
// ou compilar:
//
//	go build -o checker.exe . && ./checker.exe
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
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// endpoint que valida se um username pode ser criado.
	// code 0 = disponivel, 1 = em uso, 2 = impróprio/bloqueado, 10 = formato invalido.
	validateURL = "https://auth.roblox.com/v1/usernames/validate?birthday=2000-01-01&username=%s&context=Signup"

	// >>> DELAY POR USERNAME <<< cada username so e re-checado este tempo depois
	// da ultima checagem (por QUALQUER proxy). Ex.: 4s = cada nome a cada 4s.
	delayPorUsername = 15 * time.Second

	// teto de requests/min POR PROXY (o limite do IP e ~1000). Cada proxy manda
	// quantas quiser ate esse teto.
	proxyRateLimit = 1000

	// quantas requests a proxy mantem EM VOO ao mesmo tempo. Serve so pra nao
	// ficar presa na latencia (com 1, a proxy fica em ~1/latencia e nao alcanca o
	// teto). O ticker garante que MESMO ASSIM nao passa de proxyRateLimit/min.
	// 4 satura o teto pra latencia ate ~240ms; suba se suas proxies forem lentas.
	// OBS: proxies datacenter/sticky (flashproxy) podem limitar conexoes
	// concorrentes -> se der timeout em massa, volte pra 1.
	requestsEmVooPorProxy = 2

	// STARTUP LEVE: sobe as proxies aos poucos (N por segundo) pra nao dar
	// thundering-herd de TLS handshake (que causava "handshake timeout").
	startupRampPerSec = 300

	timeout = 10 * time.Second
	// UA curto de proposito: menos bytes por request (a request cheia era ~120B).
	userAgent = "Mozilla/5.0"

	retriesTransient = 1 // re-tentativas em qualquer erro (conexao ou status != 200)

	usernamesFile = "usernames.txt"
	proxiesFile   = "proxies.txt"
	outputFile    = "available.txt"

	// MODO MONITORAMENTO: true = fica varrendo a lista em loop infinito (ciclos),
	// pra pegar um nome no instante que liberar. false = uma passada so e termina.
	loopForever = true
	// pula os nomes ja achados disponiveis nos proximos ciclos (nao adianta
	// re-checar um nome que ja esta livre).
	skipFound = true

	// NOTIFICACAO DISCORD: quando true, envia cada username disponivel pra webhook
	// abaixo marcando @everyone. Envio direto (sem proxy), com 1 worker serializado
	// que respeita o rate limit do Discord.
	webhookEnabled = true
	discordWebhook = "https://discord.com/api/webhooks/1525733217343111238/i6DdiTJyHQbJw02dWg6HHUbqPKiTwzk_TpXOEd4yZGfsyeO6sc0ZxBcu1xf26U7EhIFz"

	// AUTO-CLAIM: quando true, tenta trocar seu username para o nome disponivel
	// (custa 1000 Robux por troca).
	// ATENCAO: a troca NAO fica em auth.roblox.com/v2/usernames (essa rota nao
	// existe -> 404 em tudo, inclusive no pre-flight do CSRF). O endpoint certo e
	// POST https://auth.roblox.com/v2/username (SINGULAR) com {username,password}.
	autoClaimEnabled    = true
	changeUsernameURL   = "https://auth.roblox.com/v2/username"
	roblosecurityCookie = "_|WARNING:-DO-NOT-SHARE-THIS.--Sharing-this-will-allow-someone-to-log-in-as-you-and-to-steal-your-ROBUX-and-items.|_CAEQAhoGCAIQBBgBIhsKBGR1aWQSEzc3NDM2NDYyODIwNTUyNzgyNTAiFQoFdW5hbWUSDHRvb3Nsb3dfMDAwMiISCgN1aWQSCzExNjgzNjUwNzE1KAM.KOYmQ8mCtmwx7EjhgwSfPvx82x4wvoKtw9Rr3lpohtnFP7_CBxFLW6wpAdXfZvz_ctK2xnPnTUHxwygjWsgVDPC5JxKQuRtOkWWmOyc89sQ30Cp6zb5PcJEptHSsQs6hnH5e2fvGFAIXDNuZYMI9XWIrQHBCCLfhau-n0zh4BWogGK5sCA5YWOmA1C3KuQZmJBNNMoDB0R7ZVgaKJtmsd-mEIW17-Nki1DQvLNz3dBEEYJVydk2bI6Dr8SwL5pK2-_S9RalIaF-ikqWixpAhsxlRaGRGACkQKc6g7KRf70K9ZbHl-N0shkzvlUhv6lfZ3Zey0BiKh9LKWFl4BJFw0EXoh1ArPN7Y5OheswYkKsYpc6fBbyzI7bp_6nhcR8zC7RTWkvxuGrQobv5gI8riUg1lwieaCw_9sXcvvsAWksc5XH3P9eQRzvE26irRFNHmIf7fg_OgoKKlRLfnrjUcnow0nW3wGN59Gk2-97BpbdFIQrRRX6XqjB1DsZYPdODnjgfbIFZnfeLHgcymLL4u4MSJVjIFtOIgcgjMXG5VFiSb-T7S-xm8A_mVUAsiQqdiakJm0iGUamDGlKLM3YBNnLq22CR5g44O0d3OOWUuopYZnExzEqkBMWrKArVnxXK02RAPf2GmLmDtqi1Jwhp7bAQQUc5kAiPyIEDEqGrpJEx0qEZvUacILjBQXxmKdtRIy3JCUxu0Z1Vzb2Cn3_0-SaTWQWeTZsOwZPmFVDDw2yvba6_LbxdvcKnKb3sybNnGdBz-yLTxTCk3I2t3E7iaze2bmxtwYpSrkawfDuh9JMyEGdLcakHGH001JMUrSEKa2hiRSYv9KZFfSt2Ft6mXrEz46FYEWeXBJYvVAjiP7y6qnnTxdvTpiWPKZmuFwqEjonxZjus2fCLQr04nS135qA.mORwmLaLkoWobOrgrph9cu1MzcY"
	robloxPassword      = "SenhaGG123!"
)

// intervalo minimo entre requests de UMA proxy (o teto de proxyRateLimit).
const proxyMinInterval = time.Minute / proxyRateLimit

// cache de sessao TLS COMPARTILHADO por todas as proxies: permite handshake
// abreviado (resumption) nas reconexoes, cortando bytes/tempo de TLS.
var tlsSessionCache = tls.NewLRUClientSessionCache(4096)

var (
	checkedCount   atomic.Int64
	availableCount atomic.Int64
	takenCount     atomic.Int64
	blockedCount   atomic.Int64
	errorCount     atomic.Int64
	startTime      time.Time
	// nomes ja achados disponiveis (pra dedupe da saida e skip nos proximos ciclos)
	foundAvailable sync.Map
	// diagnostico: proxies que realmente entraram em operacao e latencia media.
	activeProxies atomic.Int64 // proxies com client valido, em loop
	latSumMs      atomic.Int64 // soma acumulada das latencias (ms)
	latCount      atomic.Int64 // qtd de requests medidas
)

// CPM/EPM contados em JANELA DESLIZANTE de 60s, atualizada A CADA request.
// perMinute() = quantos eventos aconteceram nos ultimos 60 segundos = a taxa real.
var (
	cpmWin rateWindow // requests validas por minuto
	epmWin rateWindow // erros de conexao por minuto
)

// rateWindow: 60 buckets de 1s. add() marca o evento no segundo atual e zera os
// segundos que ja sairam da janela; perMinute() soma os 60 buckets.
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
		r.buckets = [60]int64{} // passou +1min sem eventos: zera tudo
	} else {
		for s := r.lastSec + 1; s <= now; s++ {
			r.buckets[s%60] = 0 // zera cada segundo que passou
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

// markValid/markError: chamados a cada request -> total + janela deslizante.
func markValid() { checkedCount.Add(1); cpmWin.add() }
func markError() { errorCount.Add(1); epmWin.add() }

type validateResp struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ---- log compacto (uma linha por username) ----

// useColor liga/desliga as cores ANSI. Desligue se for redirecionar pra arquivo.
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

// logLine imprime uma linha compacta e alinhada: TAG  username  detalhe
func logLine(color, tag, username string, detail string) {
	tagStr := colorize(color, fmt.Sprintf("%-5s", tag))
	rate := colorize(cGray, fmt.Sprintf("cpm:%d epm:%d", cpmWin.perMinute(), epmWin.perMinute()))
	if detail == "" {
		fmt.Printf("%s %-16s %s\n", tagStr, username, rate)
		return
	}
	fmt.Printf("%s %-16s %-22s %s\n", tagStr, username, detail, rate)
}

// trimErr reduz o erro ao motivo essencial (tira o "Get \"url\":" ruidoso).
func trimErr(err error) string {
	s := err.Error()
	if i := strings.LastIndex(s, ": "); i >= 0 && i+2 < len(s) {
		s = s[i+2:]
	}
	return s
}

// ---- notificacao Discord ----

// fila de usernames pra notificar; 1 worker consome e serializa os envios.
var webhookQueue = make(chan string, 256)

// client dedicado da webhook: envio DIRETO (sem proxy), separado do checker.
var webhookClient = &http.Client{Timeout: 15 * time.Second}

// webhookWorker consome a fila e posta cada nome, respeitando rate limit.
func webhookWorker() {
	for username := range webhookQueue {
		postWebhook(username)
	}
}

// postWebhook manda 1 mensagem marcando @everyone; trata 429 (retry_after).
func postWebhook(username string) {
	payload := map[string]any{
		"content":          fmt.Sprintf("@everyone\n✅ **Username disponivel:** `%s`", username),
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
			// respeita o retry_after informado pelo Discord
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
	claimMu sync.Mutex
	// claimStarted trava ANTES da request: sem isso, dois nomes achados livres no
	// mesmo instante disparam dois claims e a conta paga 1000 Robux DUAS vezes
	// (alreadyClaimed so era setado depois do 200, tarde demais).
	claimStarted   bool
	alreadyClaimed bool
)

// client dedicado do claim (direto, sem proxy) — separado do da webhook.
var claimClient = &http.Client{Timeout: 20 * time.Second}

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

// newClaimReq monta o POST da troca. csrf vazio = request de pre-flight (o
// Roblox responde 403 com o header X-CSRF-TOKEN, que reenviamos na 2a chamada).
func newClaimReq(payload []byte, csrf string) (*http.Request, error) {
	req, err := http.NewRequest(http.MethodPost, changeUsernameURL, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json;charset=UTF-8")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Cookie", ".ROBLOSECURITY="+roblosecurityCookie)
	req.Header.Set("User-Agent", chefUserAgent)
	req.Header.Set("Origin", "https://www.roblox.com")
	req.Header.Set("Referer", "https://www.roblox.com/")
	req.Header.Set("Sec-Ch-Ua", `"Chromium";v="138", "Google Chrome";v="138", "Not-A.Brand";v="24"`)
	req.Header.Set("Sec-Ch-Ua-Mobile", "?0")
	req.Header.Set("Sec-Ch-Ua-Platform", `"Windows"`)
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Site", "same-site")
	if csrf != "" {
		req.Header.Set("X-CSRF-TOKEN", csrf)
	}
	return req, nil
}

// claimFail loga + notifica e, quando a falha comprovadamente NAO gastou Robux,
// libera a trava pra tentar no proximo nome livre.
func claimFail(username, detail, msg string, releaseLock bool) {
	if releaseLock {
		claimMu.Lock()
		if !alreadyClaimed {
			claimStarted = false
		}
		claimMu.Unlock()
	}
	logLine(cRed, "CLAIM", username, detail)
	notifyClaim(username, msg)
}

func claimUsername(username string) {
	claimMu.Lock()
	if claimStarted {
		claimMu.Unlock()
		logLine(cGray, "CLAIM", username, "troca ja em andamento/feita")
		return
	}
	claimStarted = true // trava ANTES de sair a request (evita gasto duplo)
	claimMu.Unlock()

	payload, _ := json.Marshal(map[string]string{
		"username": username,
		"password": robloxPassword,
	})

	// --- 1) pre-flight: pega o X-CSRF-TOKEN (403 esperado aqui) ---
	req, err := newClaimReq(payload, "")
	if err != nil {
		claimFail(username, "erro montando request: "+trimErr(err), fmt.Sprintf("❌ **Falha ao trocar username para** `%s`\n**Motivo:** %s", username, trimErr(err)), true)
		return
	}
	resp, err := claimClient.Do(req)
	if err != nil {
		reason := trimErr(err)
		claimFail(username, "erro: "+reason, fmt.Sprintf("❌ **Falha ao trocar username para** `%s`\n**Motivo:** erro de conexao — %s", username, reason), true)
		return
	}
	csrfToken := resp.Header.Get("X-Csrf-Token")
	firstBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	firstStatus := resp.StatusCode
	resp.Body.Close()

	// o pre-flight ja pode ter trocado o nome se o cookie tinha token valido em
	// cache do lado do Roblox — na pratica ele responde 403 + token.
	if firstStatus == http.StatusOK {
		claimMu.Lock()
		alreadyClaimed = true
		claimMu.Unlock()
		logLine(cGreen, "CLAIM", username, "USERNAME TROCADO COM SUCESSO!")
		notifyClaim(username, fmt.Sprintf("✅ **Username trocado com sucesso para** `%s`!", username))
		return
	}

	if csrfToken == "" {
		hint := ""
		switch firstStatus {
		case http.StatusNotFound:
			hint = " (404: URL da troca errada — use auth.roblox.com/v2/username, no singular)"
		case http.StatusUnauthorized:
			hint = " (401: .ROBLOSECURITY invalido/expirado)"
		}
		detail := fmt.Sprintf("sem CSRF, status %d%s: %s", firstStatus, hint, string(firstBody))
		claimFail(username, detail, fmt.Sprintf("❌ **Falha ao trocar username para** `%s`\n**Status:** %d%s\n**Resposta:** %s", username, firstStatus, hint, string(firstBody)), true)
		return
	}

	// --- 2) troca de verdade, agora com o token ---
	// O token pode rotacionar/expirar entre o pre-flight e este POST. Quando isso
	// acontece o Roblox responde 403 "Token Validation Failed" JA COM o token novo
	// no header X-Csrf-Token: basta reenviar com ele (1 vez, pra nao virar loop).
	csrfRetried := false
	for {
		req, err = newClaimReq(payload, csrfToken)
		if err != nil {
			claimFail(username, "erro montando request: "+trimErr(err), fmt.Sprintf("❌ **Falha ao trocar username para** `%s`\n**Motivo:** %s", username, trimErr(err)), true)
			return
		}
		resp, err = claimClient.Do(req)
		if err != nil {
			// erro de rede DEPOIS do envio: pode ter passado no servidor -> nao libera
			// a trava, pra nao arriscar pagar 1000 Robux de novo.
			reason := trimErr(err)
			claimFail(username, "erro no claim (estado incerto): "+reason, fmt.Sprintf("⚠️ **Troca para** `%s` **com resultado desconhecido**\n**Motivo:** %s — confira a conta manualmente", username, reason), false)
			return
		}
		// 403 SEM rblx-challenge-id = CSRF, nao 2FA. Com token novo no header,
		// reenvia; a request anterior nao debitou nada (foi rejeitada na borda).
		if resp.StatusCode == http.StatusForbidden && resp.Header.Get("Rblx-Challenge-Id") == "" && !csrfRetried {
			if newTok := resp.Header.Get("X-Csrf-Token"); newTok != "" && newTok != csrfToken {
				resp.Body.Close()
				csrfToken = newTok
				csrfRetried = true
				logLine(cYellow, "CLAIM", username, "CSRF rotacionou, reenviando com token novo")
				continue
			}
		}
		break
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))

	switch {
	case resp.StatusCode == http.StatusOK:
		claimMu.Lock()
		alreadyClaimed = true
		claimMu.Unlock()
		logLine(cGreen, "CLAIM", username, "USERNAME TROCADO COM SUCESSO!")
		notifyClaim(username, fmt.Sprintf("✅ **Username trocado com sucesso para** `%s`!", username))

	case resp.StatusCode == http.StatusForbidden && resp.Header.Get("Rblx-Challenge-Id") != "":
		// challenge do Roblox. So o "chef" e resolvido automaticamente aqui.
		challengeID := resp.Header.Get("Rblx-Challenge-Id")
		challengeType := resp.Header.Get("Rblx-Challenge-Type")
		challengeMeta := resp.Header.Get("Rblx-Challenge-Metadata")

		if !strings.EqualFold(challengeType, "chef") {
			// blocksession / 2FA por email / captcha: precisa de acao humana.
			detail := fmt.Sprintf("challenge '%s' nao automatizavel — resolva manualmente", challengeType)
			claimFail(username, detail, fmt.Sprintf("❌ **Falha ao trocar username para** `%s`\n**Motivo:** challenge `%s` (ação manual necessária)", username, challengeType), true)
			break
		}

		logLine(cYellow, "CLAIM", username, "challenge chef recebido, resolvendo...")
		retryType, retryMeta, err := solveChef(csrfToken, challengeID, challengeMeta)
		if err != nil {
			// falha ANTES de reenviar o claim -> nao debitou -> libera a trava.
			claimFail(username, "chef falhou: "+trimErr(err), fmt.Sprintf("❌ **Falha ao trocar username para** `%s`\n**Motivo:** solver do chef falhou — %s", username, trimErr(err)), true)
			break
		}

		// chef resolvido: reenvia o POST /v2/username com os headers rblx-challenge-*.
		// O type e o metadata vem do solveChef (twostepverification + prova/metadata),
		// seguindo o fluxo do roblox2fapayout — NAO type "chef".
		req2, err := newClaimReq(payload, csrfToken)
		if err != nil {
			claimFail(username, "erro montando reenvio: "+trimErr(err), fmt.Sprintf("❌ **Falha ao trocar username para** `%s`\n**Motivo:** %s", username, trimErr(err)), true)
			break
		}
		req2.Header.Set("Rblx-Challenge-Id", challengeID)
		req2.Header.Set("Rblx-Challenge-Type", retryType)
		req2.Header.Set("Rblx-Challenge-Metadata", retryMeta)

		resp2, err := claimClient.Do(req2)
		if err != nil {
			// erro de rede depois do reenvio: estado incerto -> nao libera a trava.
			claimFail(username, "reenvio pos-chef (estado incerto): "+trimErr(err), fmt.Sprintf("⚠️ **Troca para** `%s` **com resultado desconhecido**\n**Motivo:** %s — confira a conta manualmente", username, trimErr(err)), false)
			break
		}
		body2, _ := io.ReadAll(io.LimitReader(resp2.Body, 8192))
		resp2.Body.Close()

		if resp2.StatusCode == http.StatusOK {
			claimMu.Lock()
			alreadyClaimed = true
			claimMu.Unlock()
			logLine(cGreen, "CLAIM", username, "USERNAME TROCADO COM SUCESSO! (pos-chef)")
			notifyClaim(username, fmt.Sprintf("✅ **Username trocado com sucesso para** `%s`! (chef resolvido)", username))
		} else {
			releaseLock := resp2.StatusCode == http.StatusBadRequest // 400 nao debita
			claimFail(username, fmt.Sprintf("reenvio pos-chef falhou %d: %s", resp2.StatusCode, string(body2)), fmt.Sprintf("❌ **Falha ao trocar username para** `%s` (pos-chef)\n**Status:** %d\n**Resposta:** %s", username, resp2.StatusCode, string(body2)), releaseLock)
		}

	case resp.StatusCode == http.StatusBadRequest:
		// 400 = recusa validada (senha errada, nome ja pego, saldo insuficiente,
		// filtro): nao debitou nada, entao libera pro proximo nome.
		claimFail(username, fmt.Sprintf("recusado 400: %s", string(respBody)), fmt.Sprintf("❌ **Falha ao trocar username para** `%s`\n**Status:** 400\n**Resposta:** %s", username, string(respBody)), true)

	default:
		claimFail(username, fmt.Sprintf("falhou %d: %s", resp.StatusCode, string(respBody)), fmt.Sprintf("❌ **Falha ao trocar username para** `%s`\n**Status:** %d\n**Resposta:** %s", username, resp.StatusCode, string(respBody)), false)
	}
}

// ---- reaproveitado do proxytester (main.go) ----

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
		// HTTP/2 desligado: proxies rotativos com CONNECT quebram em h2 (EOF).
		ForceAttemptHTTP2: false,
		TLSNextProto:      map[string]func(string, *tls.Conn) http.RoundTripper{},
		// TLS 1.3 + cache de sessao COMPARTILHADO por todas as proxies: apos o 1o
		// handshake completo, novas conexoes (mesmo por outra proxy) fazem handshake
		// ABREVIADO (resumption) -> muito menos bytes/tempo por reconexao.
		// (se comecar a falhar handshake em massa, troque por tls.VersionTLS12)
		TLSClientConfig: &tls.Config{
			MinVersion:         tls.VersionTLS13,
			ClientSessionCache: tlsSessionCache,
		},
		// varias requests em voo por proxy -> ate requestsEmVooPorProxy conexoes,
		// reusadas via keep-alive (o cache de sessao TLS barateia as extras).
		MaxIdleConns:        requestsEmVooPorProxy,
		MaxIdleConnsPerHost: requestsEmVooPorProxy,
		MaxConnsPerHost:     requestsEmVooPorProxy,
		// conexao ociosa segura por muito mais tempo -> menos reconexao/handshake.
		IdleConnTimeout:       5 * time.Minute,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		DisableKeepAlives:     false,
	}
	return &http.Client{Transport: transport, Timeout: timeout}, nil
}

// ---- checagem ----

// checkOnce faz UMA request e devolve (statusHTTP, code, erro).
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

// checkUsername checa 1 username com ate retriesTransient retries. Conta POR
// TENTATIVA: toda request que erra (QUALQUER erro, ou status != 200) vira EPM;
// toda resposta 200 valida vira CPM. Assim nenhum erro escapa da contagem, nem
// os desconhecidos (ex.: "timeout awaiting response headers").
func checkUsername(client *http.Client, proxy, username string, availableOut chan<- string) {
	for attempt := 0; ; attempt++ {
		reqStart := time.Now()
		status, code, err := checkOnce(client, username)
		latSumMs.Add(time.Since(reqStart).Milliseconds())
		latCount.Add(1)

		// QUALQUER erro de conexao conta (timeout, EOF, reset, desconhecido...).
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
		// status != 200 (429, 5xx, etc) tambem conta como erro.
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

		// resposta 200 valida -> classifica pelo code
		markValid()
		switch code {
		case 0:
			if _, dup := foundAvailable.LoadOrStore(username, true); !dup {
				availableCount.Add(1)
				logLine(cGreen, "LIVRE", username, "*** NOVO ***")
				availableOut <- username
				if autoClaimEnabled {
					go claimUsername(username)
				}
				if webhookEnabled {
					select {
					case webhookQueue <- username:
					default: // fila cheia: nao trava a checagem
						logLine(cGray, "HOOK", username, "fila da webhook cheia, pulado")
					}
				}
			} else {
				logLine(cGreen, "LIVRE", username, "")
			}
		case 1:
			takenCount.Add(1)
			logLine(cRed, "USADO", username, "")
		default: // code 2 (improprio), 10 (formato) e outros bloqueios
			blockedCount.Add(1)
			logLine(cCyan, "BLOCK", username, "")
		}
		return
	}
}

func short(proxy string) string {
	// mostra so o host da proxy no log, sem credenciais
	if u, err := url.Parse(normalizeProxy(proxy)); err == nil && u.Host != "" {
		return u.Host
	}
	if i := strings.LastIndex(proxy, "@"); i >= 0 {
		return proxy[i+1:]
	}
	return proxy
}

// runProxy: roda requestsEmVooPorProxy goroutines por proxy, TODAS gated pelo
// mesmo ticker (proxyMinInterval) — entao a proxy nunca passa de proxyRateLimit/min,
// mas com varias requests em voo a latencia nao serializa e ela satura o teto de
// 1000/min (em vez de ficar presa em ~1/latencia). Depois de checar, reschedule
// re-agenda o nome pra daqui delayPorUsername.
func runProxy(proxy string, jobs <-chan string, reschedule func(string), availableOut chan<- string, wg *sync.WaitGroup) {
	defer wg.Done()

	client, err := newProxyClient(proxy)
	if err != nil {
		fmt.Printf("[PROXY INVALIDA] %s -> %v\n", short(proxy), err)
		return
	}
	defer client.CloseIdleConnections()
	activeProxies.Add(1) // essa proxy entrou em operacao

	ticker := time.NewTicker(proxyMinInterval)
	defer ticker.Stop()

	var sub sync.WaitGroup
	for i := 0; i < requestsEmVooPorProxy; i++ {
		sub.Add(1)
		go func() {
			defer sub.Done()
			for username := range jobs {
				<-ticker.C // teto compartilhado: <= proxyRateLimit/min por proxy
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

	// saida: available.txt (flush a cada nome pra nao perder progresso)
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

	// worker da webhook do Discord (serializa envios e respeita rate limit)
	if webhookEnabled {
		go webhookWorker()
	}

	// FILA POR USERNAME: cada nome, depois de checado, so volta pra fila daqui
	// delayPorUsername (4s). Assim cada username e re-checado a cada ~4s por
	// QUALQUER proxy. As proxies puxam daqui, cada uma limitada a proxyRateLimit.
	jobs := make(chan string, len(usernames)+1)
	var remaining atomic.Int64
	remaining.Store(int64(len(usernames)))

	// reschedule: chamado depois de checar um nome. No modo loop, re-enfileira
	// esse nome daqui delayPorUsername. Em passada unica, conta ate zerar e fecha.
	reschedule := func(u string) {
		if !loopForever {
			if remaining.Add(-1) == 0 {
				close(jobs)
			}
			return
		}
		if skipFound {
			if _, ok := foundAvailable.Load(u); ok {
				return // ja achado livre: sai de rotacao
			}
		}
		time.AfterFunc(delayPorUsername, func() { jobs <- u })
	}

	// seed: todos comecam "vencidos" (elegiveis pra checar agora).
	for _, u := range usernames {
		jobs <- u
	}

	// printer de progresso
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

	// dispara 1 goroutine por proxy, mas SOBE AOS POUCOS (startupRampPerSec) pra
	// nao dar thundering-herd de TLS handshake (que causava handshake timeout).
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
	// ciclo real = quantas passadas completas ja foram efetivamente checadas
	processed := checked + errs
	cycle := processed/int64(total) + 1
	inCycle := processed % int64(total)
	avgLat := int64(0)
	if n := latCount.Load(); n > 0 {
		avgLat = latSumMs.Load() / n
	}
	line := fmt.Sprintf("── ciclo %d (%d/%d) | CPM %d · EPM %d | ativas %d | lat ~%dms | livre %d · uso %d · bloq %d · erro %d ──",
		cycle, inCycle, total, cpmWin.perMinute(), epmWin.perMinute(),
		activeProxies.Load(), avgLat,
		availableCount.Load(), takenCount.Load(), blockedCount.Load(), errs)
	fmt.Println(colorize(cYellow, line))
}
