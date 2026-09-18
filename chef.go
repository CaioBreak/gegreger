package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/x509"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const (
	fetchURLfmt     = "https://apis.roblox.com/rotating-client-service/v1/fetch?challengeId=%s&identifier=%s"
	submitURL       = "https://apis.roblox.com/rotating-client-service/v1/submit"
	chefContinueURL = "https://apis.roblox.com/challenge/v1/continue"
	verify2FAURLfmt = "https://twostepverification.roblox.com/v1/users/%s/challenges/authenticator/verify"
	authUserURL     = "https://users.roblox.com/v1/users/authenticated"

	chefUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/138.0.0.0 Safari/537.36"

	twoFactorSecret = "LSY6LQTXSYYUPBWTBBTPXTAVLM"
	chefDebug       = true
)

var chefClient = &http.Client{Timeout: 30 * time.Second}

var cachedUserID string

func chefLog(step string, status int, body []byte) {
	if !chefDebug {
		return
	}
	fmt.Printf("[CHEF] %s -> status %d | body: %s\n", step, status, strings.TrimSpace(string(body)))
}

func getAuthenticatedUserID() (string, error) {
	if cachedUserID != "" {
		return cachedUserID, nil
	}
	req, err := http.NewRequest(http.MethodGet, authUserURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Cookie", ".ROBLOSECURITY="+roblosecurityCookie)
	req.Header.Set("User-Agent", chefUserAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := chefClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))

	var user struct {
		ID json.Number `json:"id"`
	}
	if err := json.Unmarshal(body, &user); err != nil {
		return "", fmt.Errorf("parse user failed: %w (%s)", err, string(body))
	}
	if user.ID.String() == "" || user.ID.String() == "0" {
		return "", fmt.Errorf("userId is 0/empty, auth failed: %s", string(body))
	}
	cachedUserID = user.ID.String()
	if chefDebug {
		fmt.Printf("[CHEF] authenticated userId: %s\n", cachedUserID)
	}
	return cachedUserID, nil
}

func setBrowserHeaders(req *http.Request) {
	req.Header.Set("User-Agent", chefUserAgent)
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Origin", "https://www.roblox.com")
	req.Header.Set("Referer", "https://www.roblox.com/")
	req.Header.Set("Cookie", ".ROBLOSECURITY="+roblosecurityCookie)
	req.Header.Set("Sec-Ch-Ua", `"Chromium";v="138", "Google Chrome";v="138", "Not-A.Brand";v="24"`)
	req.Header.Set("Sec-Ch-Ua-Mobile", "?0")
	req.Header.Set("Sec-Ch-Ua-Platform", `"Windows"`)
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Site", "same-site")
}

func setFetchHeaders(req *http.Request) {
	setBrowserHeaders(req)
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Connection", "keep-alive")
}

func setSubmitHeaders(req *http.Request, csrf string) {
	setBrowserHeaders(req)
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Content-Type", "application/json-patch+json")
	req.Header.Set("X-CSRF-TOKEN", csrf)
	req.Header.Set("Connection", "keep-alive")
}

func setContinueHeaders(req *http.Request, csrf string) {
	setBrowserHeaders(req)
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Content-Type", "application/json;charset=UTF-8")
	req.Header.Set("X-CSRF-TOKEN", csrf)
}

func buildFingerprintData(symbolEntry string) map[string]any {
	return map[string]any{
		"symbolEntry": symbolEntry,
		"events": []any{
			map[string]any{
				"audio": map[string]any{
					"sampleHash":       1168.9068228197468,
					"oscillator":       "sine",
					"maxChannels":      1,
					"channelCountMode": "max",
				},
				"canvas": map[string]any{
					"commonImageDataHash": "6999abd310347a74500b74b30bb97077",
				},
				"fonts": map[string]any{
					"Arial Black":           531.9140625,
					"Calibri":               420.046875,
					"Candara":               435.4453125,
					"Comic Sans MS":         462.4453125,
					"Constantia":            469.86328125,
					"Courier":               432.0703125,
					"Courier New":           432.0703125,
					"Franklin Gothic Medium": 431.82421875,
					"Georgia":               475.2421875,
					"Impact":                395.54296875,
					"Lucida Console":        433.828125,
					"Lucida Sans Unicode":   472.0078125,
					"Segoe Print":           514.30078125,
					"Segoe Script":          525.234375,
					"Segoe UI":              450.0,
					"Tahoma":                432.45703125,
					"Trebuchet MS":          428.90625,
					"Verdana":               486.5625,
				},
				"hardware": map[string]any{
					"videocard": map[string]any{
						"vendor":                 "WebKit",
						"renderer":               "WebKit WebGL",
						"version":                "WebGL 1.0 (OpenGL ES 2.0 Chromium)",
						"shadingLanguageVersion": "WebGL GLSL ES 1.0 (OpenGL ES GLSL ES 1.0 Chromium)",
					},
					"architecture":     255,
					"deviceMemory":     "8",
					"jsHeapSizeLimit":  4294705152,
				},
				"locales": map[string]any{
					"languages": "en-US",
					"timezone":  "America/Chicago",
				},
				"permissions": map[string]any{
					"accelerometer":    "granted",
					"backgroundFetch":  "granted",
					"backgroundSync":   "granted",
					"camera":           "prompt",
					"clipboardRead":    "prompt",
					"clipboardWrite":   "granted",
					"displayCapture":   "prompt",
					"gyroscope":        "granted",
					"geolocation":      "prompt",
					"localFonts":       "prompt",
					"magnetometer":     "granted",
					"microphone":       "prompt",
					"midi":             "prompt",
					"notifications":    "prompt",
					"paymentHandler":   "granted",
					"persistentStorage": "prompt",
					"storageAccess":    "granted",
					"windowManagement": "prompt",
				},
				"plugins": map[string]any{
					"plugins": []string{
						"PDF Viewer|internal-pdf-viewer|Portable Document Format",
						"Chrome PDF Viewer|internal-pdf-viewer|Portable Document Format",
						"Chromium PDF Viewer|internal-pdf-viewer|Portable Document Format",
						"Microsoft Edge PDF Viewer|internal-pdf-viewer|Portable Document Format",
						"WebKit built-in PDF|internal-pdf-viewer|Portable Document Format",
					},
				},
				"screen": map[string]any{
					"is_touchscreen": false,
					"maxTouchPoints": 0,
					"colorDepth":     24,
					"mediaMatches": []string{
						"prefers-contrast: no-preference",
						"any-hover: hover",
						"any-pointer: fine",
						"pointer: fine",
						"hover: hover",
						"update: fast",
						"prefers-reduced-motion: no-preference",
						"prefers-reduced-transparency: no-preference",
						"scripting: enabled",
						"forced-colors: none",
					},
				},
				"system": map[string]any{
					"platform":             "Win32",
					"cookieEnabled":        true,
					"productSub":           "20030107",
					"product":              "Gecko",
					"useragent":            chefUserAgent,
					"hardwareConcurrency":  12,
					"browser": map[string]any{
						"name":    "Chrome",
						"version": "138.0",
					},
					"applePayVersion": 0,
				},
				"webgl": map[string]any{
					"commonImageHash": "3a4ed1c6378f68583893dd719f84f6c9",
				},
				"math": map[string]any{
					"acos":     1.0471975511965979,
					"asin":     -9.614302481290016e-17,
					"atan":     4.578239276804769e-17,
					"cos":      -4.854249971455313e-16,
					"cosh":     1.9468519159297506,
					"e":        2.718281828459045,
					"largeCos": 0.7639704044417283,
					"largeSin": -0.6452512852657808,
					"largeTan": -0.8446024630198843,
					"log":      6.907755278982137,
					"pi":       3.141592653589793,
					"sin":      -1.9461946644816207e-16,
					"sinh":     -0.6288121810679035,
					"sqrt":     1.4142135623730951,
					"tan":      6.980860926542689e-14,
					"tanh":     -0.39008295789884684,
				},
				"data_latency_ms": 156.89999999850988,
			},
		},
		"metrics": []any{},
	}
}

func produceProtectedPayload(publicKeyB64 string, payload map[string]any) (keyB64, eventPayloadB64, ivB64 string, err error) {
	aesKey := make([]byte, 32)
	if _, err := rand.Read(aesKey); err != nil {
		return "", "", "", err
	}

	derBytes, err := base64.StdEncoding.DecodeString(publicKeyB64)
	if err != nil {
		return "", "", "", fmt.Errorf("invalid public key base64: %w", err)
	}
	pubKeyIface, err := x509.ParsePKIXPublicKey(derBytes)
	if err != nil {
		return "", "", "", fmt.Errorf("invalid public key DER: %w", err)
	}
	rsaPubKey, ok := pubKeyIface.(*rsa.PublicKey)
	if !ok {
		return "", "", "", fmt.Errorf("not an RSA public key")
	}

	wrappedKey, err := rsa.EncryptOAEP(sha1.New(), rand.Reader, rsaPubKey, aesKey, nil)
	if err != nil {
		return "", "", "", fmt.Errorf("RSA-OAEP encrypt: %w", err)
	}

	iv := make([]byte, 12)
	if _, err := rand.Read(iv); err != nil {
		return "", "", "", err
	}

	block, err := aes.NewCipher(aesKey)
	if err != nil {
		return "", "", "", err
	}
	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", "", "", err
	}

	plaintext, _ := json.Marshal(payload)
	ciphertext := aesgcm.Seal(nil, iv, plaintext, nil)

	return base64.StdEncoding.EncodeToString(wrappedKey),
		base64.StdEncoding.EncodeToString(ciphertext),
		base64.StdEncoding.EncodeToString(iv),
		nil
}

var (
	reToken  = regexp.MustCompile(`produceProtectedPayload\s*\(\s*"([^"]+)"`)
	reSymbol = regexp.MustCompile(`expectedSymbol="([^"]+)"`)
)

func fetchAndSubmitChallenge(csrf, challengeID, identifier, userID string) error {
	fetchURL := fmt.Sprintf(fetchURLfmt, challengeID, identifier)
	req, err := http.NewRequest(http.MethodGet, fetchURL, nil)
	if err != nil {
		return err
	}
	setFetchHeaders(req)

	resp, err := chefClient.Do(req)
	if err != nil {
		return fmt.Errorf("fetch: %w", err)
	}
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 131072))
	resp.Body.Close()
	chefLog("fetch("+identifier[:8]+"...)", resp.StatusCode, respBody[:min(200, len(respBody))])

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch status %d: %s", resp.StatusCode, string(respBody))
	}

	jsCode, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(respBody)))
	if err != nil {
		return fmt.Errorf("fetch response not base64: %w", err)
	}
	jsStr := string(jsCode)

	tokenMatch := reToken.FindStringSubmatch(jsStr)
	if tokenMatch == nil {
		return fmt.Errorf("challenge_token not found in JS (%d bytes)", len(jsStr))
	}
	challengeToken := tokenMatch[1]

	symbolMatch := reSymbol.FindStringSubmatch(jsStr)
	if symbolMatch == nil {
		return fmt.Errorf("expectedSymbol not found in JS (%d bytes)", len(jsStr))
	}
	expectedSymbol := symbolMatch[1]

	if chefDebug {
		tkPreview := challengeToken
		if len(tkPreview) > 40 {
			tkPreview = tkPreview[:40] + "..."
		}
		fmt.Printf("[CHEF] expectedSymbol=%s | token=%s\n", expectedSymbol, tkPreview)
	}

	fingerprintData := buildFingerprintData(expectedSymbol)
	fingerprintJSON, _ := json.Marshal(fingerprintData)

	fPayload := map[string]any{
		"symbolEntry": expectedSymbol,
		"events":      []string{string(fingerprintJSON)},
		"metrics":     []any{},
	}

	key, eventPayload, iv, err := produceProtectedPayload(challengeToken, fPayload)
	if err != nil {
		return fmt.Errorf("encrypt: %w", err)
	}

	submitBody, _ := json.Marshal(map[string]any{
		"userId":      userID,
		"challengeId": challengeID,
		"payloadV2":   eventPayload,
		"params": map[string]string{
			"key": key,
			"iv":  iv,
		},
		"btid": "0",
	})

	req, err = http.NewRequest(http.MethodPost, submitURL, strings.NewReader(string(submitBody)))
	if err != nil {
		return err
	}
	setSubmitHeaders(req, csrf)

	resp, err = chefClient.Do(req)
	if err != nil {
		return fmt.Errorf("submit: %w", err)
	}
	submitResp, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	resp.Body.Close()
	chefLog("submit("+identifier[:8]+"...)", resp.StatusCode, submitResp)

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("submit status %d: %s", resp.StatusCode, string(submitResp))
	}

	return nil
}

func totpNow(secret string) (string, error) {
	secret = strings.ToUpper(strings.ReplaceAll(secret, " ", ""))
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil {
		return "", fmt.Errorf("TOTP secret invalid (base32): %w", err)
	}
	counter := uint64(time.Now().Unix()) / 30
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], counter)

	h := hmac.New(sha1.New, key)
	h.Write(buf[:])
	sum := h.Sum(nil)

	offset := sum[len(sum)-1] & 0x0f
	code := (uint32(sum[offset]&0x7f) << 24) |
		(uint32(sum[offset+1]) << 16) |
		(uint32(sum[offset+2]) << 8) |
		uint32(sum[offset+3])
	return fmt.Sprintf("%06d", code%1_000_000), nil
}

func verify2FA(csrf, userID, challengeID string) (string, error) {
	code, err := totpNow(twoFactorSecret)
	if err != nil {
		return "", err
	}
	body, _ := json.Marshal(map[string]any{
		"actionType":  "Generic",
		"challengeId": challengeID,
		"code":        code,
	})
	url := fmt.Sprintf(verify2FAURLfmt, userID)
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(string(body)))
	if err != nil {
		return "", err
	}
	setContinueHeaders(req, csrf)

	resp, err := chefClient.Do(req)
	if err != nil {
		return "", err
	}
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	resp.Body.Close()
	chefLog("verify(authenticator)", resp.StatusCode, respBody)

	var vr struct {
		VerificationToken string `json:"verificationToken"`
		Errors            []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	_ = json.Unmarshal(respBody, &vr)
	if len(vr.Errors) > 0 {
		return "", fmt.Errorf("2FA rejected: %s", vr.Errors[0].Message)
	}
	if resp.StatusCode != http.StatusOK || vr.VerificationToken == "" {
		return "", fmt.Errorf("verify status %d, no token: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return vr.VerificationToken, nil
}

func solveChef(csrf, challengeID, challengeMetaB64 string) (retryType, retryMetaB64 string, err error) {
	metaJSON, err := base64.StdEncoding.DecodeString(challengeMetaB64)
	if err != nil {
		return "", "", fmt.Errorf("metadata not base64: %w", err)
	}

	var meta map[string]any
	if err := json.Unmarshal(metaJSON, &meta); err != nil {
		return "", "", fmt.Errorf("metadata not JSON: %w (%s)", err, string(metaJSON))
	}

	userID, err := getAuthenticatedUserID()
	if err != nil {
		return "", "", fmt.Errorf("get userId: %w", err)
	}

	var continueMetaStr string
	usedRotatingService := false

	if identifiersRaw, ok := meta["scriptIdentifiers"]; ok {
		if identifiers, ok2 := identifiersRaw.([]any); ok2 && len(identifiers) > 0 {
			if chefDebug {
				fmt.Printf("[CHEF] challengeId=%s | userId=%s | %d identifiers (rotating-client-service)\n", challengeID, userID, len(identifiers))
			}
			for i, id := range identifiers {
				idStr, ok := id.(string)
				if !ok {
					return "", "", fmt.Errorf("identifier %d not string: %v", i, id)
				}
				if err := fetchAndSubmitChallenge(csrf, challengeID, idStr, userID); err != nil {
					return "", "", fmt.Errorf("part %d/%d: %w", i+1, len(identifiers), err)
				}
				fmt.Printf("[CHEF] Part %d/%d submitted OK\n", i+1, len(identifiers))
			}
			cm, _ := json.Marshal(map[string]string{
				"userId":      userID,
				"challengeId": challengeID,
			})
			continueMetaStr = string(cm)
			usedRotatingService = true
		}
	}

	if !usedRotatingService {
		if chefDebug {
			fmt.Printf("[CHEF] challengeId=%s | userId=%s | no scriptIdentifiers, continue-only flow\n", challengeID, userID)
			fmt.Printf("[CHEF] metadata: %s\n", string(metaJSON))
		}
		continueMetaStr = string(metaJSON)
	}

	continueBody, _ := json.Marshal(map[string]string{
		"challengeId":       challengeID,
		"challengeMetadata": continueMetaStr,
		"challengeType":     "chef",
	})

	req, err := http.NewRequest(http.MethodPost, chefContinueURL, strings.NewReader(string(continueBody)))
	if err != nil {
		return "", "", err
	}
	setContinueHeaders(req, csrf)

	resp, err := chefClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("continue: %w", err)
	}
	contBody, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	resp.Body.Close()
	chefLog("continue(chef)", resp.StatusCode, contBody)

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("continue status %d: %s", resp.StatusCode, string(contBody))
	}

	proofMeta, _ := json.Marshal(map[string]string{
		"userId":      userID,
		"challengeId": challengeID,
	})

	var contResp struct {
		ChallengeType     string `json:"challengeType"`
		ChallengeMetadata string `json:"challengeMetadata"`
		ChallengeID       string `json:"challengeId"`
	}
	if len(strings.TrimSpace(string(contBody))) > 0 {
		json.Unmarshal(contBody, &contResp)
	}

	switch contResp.ChallengeType {
	case "":
		return "chef", base64.StdEncoding.EncodeToString(proofMeta), nil

	case "twostepverification":
		if twoFactorSecret == "" {
			if chefDebug {
				fmt.Printf("[CHEF] 2FA required but twoFactorSecret empty; trying retry with chef metadata\n")
			}
			return "chef", base64.StdEncoding.EncodeToString(proofMeta), nil
		}

		dec := json.NewDecoder(strings.NewReader(contResp.ChallengeMetadata))
		dec.UseNumber()
		var m map[string]any
		if err := dec.Decode(&m); err != nil {
			return "", "", fmt.Errorf("2FA metadata invalid: %w", err)
		}
		uid := fmt.Sprint(m["userId"])
		nestedCID := fmt.Sprint(m["challengeId"])

		vtoken, err := verify2FA(csrf, uid, nestedCID)
		if err != nil {
			return "", "", err
		}

		m["verificationToken"] = vtoken
		m["rememberDevice"] = false
		contMeta2, _ := json.Marshal(m)

		contBody2, _ := json.Marshal(map[string]string{
			"challengeId":       challengeID,
			"challengeMetadata": string(contMeta2),
			"challengeType":     "twostepverification",
		})

		req2, err := http.NewRequest(http.MethodPost, chefContinueURL, strings.NewReader(string(contBody2)))
		if err != nil {
			return "", "", err
		}
		setContinueHeaders(req2, csrf)

		resp2, err := chefClient.Do(req2)
		if err != nil {
			return "", "", fmt.Errorf("continue 2FA: %w", err)
		}
		body2, _ := io.ReadAll(io.LimitReader(resp2.Body, 8192))
		resp2.Body.Close()
		chefLog("continue(twostepverification)", resp2.StatusCode, body2)

		if resp2.StatusCode != http.StatusOK {
			return "", "", fmt.Errorf("continue 2FA status %d: %s", resp2.StatusCode, string(body2))
		}

		var cont2Resp struct {
			ChallengeType     string `json:"challengeType"`
			ChallengeMetadata string `json:"challengeMetadata"`
		}
		if len(strings.TrimSpace(string(body2))) > 0 {
			json.Unmarshal(body2, &cont2Resp)
		}
		if cont2Resp.ChallengeType == "blocksession" {
			return "", "", fmt.Errorf("session blocked after 2FA (blocksession): %s", cont2Resp.ChallengeMetadata)
		}

		proof, _ := json.Marshal(map[string]any{
			"rememberDevice":    false,
			"actionType":        "Generic",
			"verificationToken": vtoken,
			"challengeId":       nestedCID,
		})
		return "twostepverification", base64.StdEncoding.EncodeToString(proof), nil

	case "blocksession":
		return "", "", fmt.Errorf("session blocked (blocksession) after chef: %s", contResp.ChallengeMetadata)

	default:
		return "", "", fmt.Errorf("unknown challenge after chef: %q", contResp.ChallengeType)
	}
}
