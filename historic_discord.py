"""
Historic BacBo → Discord Webhook
Conecta ao Socket.IO do HistoricIA, escuta ENTRADAS (sinais de padrões)
e RESULTADOS do Bac Bo, envia para Discord e salva histórico em arquivo.
"""

import json
import time
import threading
import sys
import os
from datetime import datetime, timezone

# Dependências de runtime (não são necessárias para o modo --analisar).
try:
    import socketio
    import cloudscraper
    import requests
except ImportError as _e:
    socketio = None
    cloudscraper = None
    requests = None
    _IMPORT_ERROR = _e
else:
    _IMPORT_ERROR = None

# ============================================================
# CONFIGURAÇÃO
# ============================================================
DISCORD_WEBHOOK_URL = "https://discord.com/api/webhooks/1392694075588874240/PftVq_6TRJe5ZIFBV5ypVvX4FI4gSSFV4BqKvQ8PoIJK334Y_FqTqXwSkNE6TFP3DV5J"

ACCESS_TOKEN = ""

EMAIL = "mivogi8742@gwshare.com"
PASSWORD = "Senha123!"

# Jogo: 1=Bac Bo, 2=Bac Bo BR, 3=Football Studio,
#        4=Futebol Studio BR, 5=Football Studio Dice
GAME_TYPE = 1

HISTORICO_RESULTADOS = "historico_resultados.json"
HISTORICO_ENTRADAS = "historico_entradas.json"

# Diagnóstico: grava o payload cru de cada evento para descobrir
# se o site realmente envia a entrada ao vivo (pendingEntry no gale 0)
# ou se só vemos o registro já resolvido. Coloque False para desligar.
DIAG = True
DIAG_LOG = "diagnostico_entradas.jsonl"

# ============================================================
GAME_NAMES = {1: "Bac Bo", 2: "Bac Bo BR", 3: "Football Studio",
              4: "Futebol Studio BR", 5: "Football Studio Dice"}
API_BASE = "https://api.historicbet.com"
ORIGIN = "https://historic.com.br"

SIGNAL_EMOJI = {"Player": "🔵", "Banker": "🔴", "Tie": "🟢"}
SIGNAL_COLOR = {"Player": 0x3B82F6, "Banker": 0xEF4444, "Tie": 0x22C55E}

def parse_signal(raw):
    """Converte signal da API (B0, P0, T, etc.) para nome legível."""
    if not raw:
        return "?"
    r = raw.upper()
    if r.startswith("B"):
        return "Banker"
    if r.startswith("P"):
        return "Player"
    if r.startswith("T"):
        return "Tie"
    return raw

# ============================================================
# HISTÓRICO LOCAL
# ============================================================

def load_json(path):
    if os.path.exists(path):
        with open(path, "r", encoding="utf-8") as f:
            return json.load(f)
    return []

def save_json(path, data):
    with open(path, "w", encoding="utf-8") as f:
        json.dump(data, f, ensure_ascii=False, indent=2)


def _slim_entry(e):
    """Extrai só os campos relevantes de uma entrada para o log."""
    if not isinstance(e, dict):
        return e
    return {
        "id": e.get("id"),
        "signal": e.get("signal"),
        "gale": e.get("gale"),
        "maxGale": e.get("maxGale"),
        "result": e.get("result"),
    }


def diag_snapshot(source, pending, entries):
    """Grava um snapshot cru do que o site enviou (pending + topo do histórico)."""
    if not DIAG:
        return
    try:
        top = [_slim_entry(e) for e in (entries or [])[:5]]
        record = {
            "ts": datetime.now(timezone.utc).isoformat(),
            "source": source,
            "pending": _slim_entry(pending) if pending else None,
            "top_entries": top,
        }
        with open(DIAG_LOG, "a", encoding="utf-8") as f:
            f.write(json.dumps(record, ensure_ascii=False) + "\n")
    except Exception as e:
        print(f"[DIAG] Erro ao gravar snapshot: {e}")


# Log cru de TODOS os eventos do Socket.IO — usado para descobrir por
# qual evento/campo o site manda a entrada AO VIVO.
RAW_LOG = "raw_events.jsonl"
_raw_counts = {}

def _shrink_for_raw(data):
    """Reduz payloads grandes (historyEntries com 20 itens) mantendo o resto."""
    if isinstance(data, dict):
        out = {}
        for k, v in data.items():
            if isinstance(v, list) and len(v) > 3:
                out[k + "__len"] = len(v)
                out[k + "__sample"] = v[:2]
            else:
                out[k] = v
        return out
    return data

def diag_raw(event, data, cap=80):
    """Grava o payload completo de um evento no raw_events.jsonl (com limite)."""
    if not DIAG:
        return
    n = _raw_counts.get(event, 0)
    if n >= cap:
        return
    _raw_counts[event] = n + 1
    try:
        with open(RAW_LOG, "a", encoding="utf-8") as f:
            f.write(json.dumps({
                "ts": datetime.now(timezone.utc).isoformat(),
                "event": event,
                "data": _shrink_for_raw(data),
            }, ensure_ascii=False, default=str) + "\n")
    except Exception as e:
        print(f"[DIAG] raw erro: {e}")

def append_resultado(resultado: dict):
    hist = load_json(HISTORICO_RESULTADOS)
    if hist and hist[0].get("id") == resultado.get("id"):
        return
    hist.insert(0, resultado)
    save_json(HISTORICO_RESULTADOS, hist)

def append_entrada(entrada: dict):
    hist = load_json(HISTORICO_ENTRADAS)
    if hist and hist[0].get("id") == entrada.get("id"):
        return
    hist.insert(0, entrada)
    save_json(HISTORICO_ENTRADAS, hist)

def save_entradas_bulk(entradas: list):
    if not entradas:
        return
    existing = load_json(HISTORICO_ENTRADAS)
    existing_ids = {e.get("id") for e in existing}
    novos = [e for e in entradas if e.get("id") not in existing_ids]
    if novos:
        merged = novos + existing
        merged.sort(key=lambda x: x.get("id", 0), reverse=True)
        save_json(HISTORICO_ENTRADAS, merged)
        print(f"[HIST] {len(novos)} entradas novas salvas ({len(merged)} total)")

# ============================================================
# CLOUDFLARE BYPASS
# ============================================================

def get_cf_cookies():
    """Usa cloudscraper para resolver o challenge do Cloudflare."""
    print("[CF] Resolvendo challenge do Cloudflare...")
    scraper = cloudscraper.create_scraper(
        browser={"browser": "chrome", "platform": "windows", "desktop": True},
    )
    try:
        resp = scraper.get(API_BASE, headers={
            "Origin": ORIGIN,
            "Referer": f"{ORIGIN}/",
        })
        cookies = scraper.cookies.get_dict()
        ua = scraper.headers.get("User-Agent", "")
        if cookies:
            print(f"[CF] Cookies obtidos: {list(cookies.keys())}")
        else:
            print("[CF] Nenhum cookie retornado (pode funcionar sem).")
        return cookies, ua, scraper
    except Exception as e:
        print(f"[CF] Erro: {e}")
        return {}, "", scraper

# ============================================================
# AUTH
# ============================================================

class AuthManager:
    def __init__(self, scraper=None):
        self.access_token = ACCESS_TOKEN or None
        self.refresh_token = None
        self.scraper = scraper or cloudscraper.create_scraper(
            browser={"browser": "chrome", "platform": "windows", "desktop": True},
        )

    def login(self):
        if not EMAIL or not PASSWORD:
            if self.access_token:
                print("[AUTH] Usando token manual.")
                return True
            print("[AUTH] ERRO: Preencha EMAIL/PASSWORD ou ACCESS_TOKEN.")
            return False
        print(f"[AUTH] Login com {EMAIL}...")
        try:
            resp = self.scraper.post(
                f"{API_BASE}/auth/login",
                json={"email": EMAIL, "password": PASSWORD},
                headers={
                    "Content-Type": "application/json",
                    "Origin": ORIGIN,
                    "Referer": f"{ORIGIN}/",
                },
            )
            data = resp.json()
            self.access_token = data.get("accessToken")
            self.refresh_token = data.get("refreshToken")
            if self.access_token:
                print("[AUTH] OK.")
                return True
            print(f"[AUTH] Falha: {data}")
            return False
        except Exception as e:
            print(f"[AUTH] Erro: {e}")
            return False

    def refresh(self):
        if not self.refresh_token:
            return self.login()
        try:
            resp = self.scraper.post(
                f"{API_BASE}/auth/refresh",
                json={"refreshToken": self.refresh_token},
                headers={
                    "Content-Type": "application/json",
                    "Origin": ORIGIN,
                    "Referer": f"{ORIGIN}/",
                },
            )
            data = resp.json()
            new_token = data.get("accessToken")
            if new_token:
                self.access_token = new_token
                self.refresh_token = data.get("refreshToken", self.refresh_token)
                print("[AUTH] Token renovado.")
                return True
        except Exception:
            pass
        return self.login()


# ============================================================
# DISCORD
# ============================================================

def discord_send(embeds: list):
    try:
        resp = requests.post(
            DISCORD_WEBHOOK_URL,
            json={"embeds": embeds},
            timeout=10,
        )
        if resp.status_code != 204:
            print(f"[DISCORD] Status {resp.status_code}: {resp.text[:200]}")
    except Exception as e:
        print(f"[DISCORD] Erro: {e}")


def discord_nova_entrada(entrada: dict):
    signal = parse_signal(entrada.get("signal"))
    gale = entrada.get("gale", 0)
    max_gale = entrada.get("maxGale", "?")
    draw_prot = entrada.get("drawProtection", False)
    emoji = SIGNAL_EMOJI.get(signal, "⚪")
    color = SIGNAL_COLOR.get(signal, 0xFFA500)
    gale_txt = "Sem Gale" if gale == 0 else f"Gale {gale}"
    game = GAME_NAMES.get(GAME_TYPE, "Bac Bo")

    embed = {
        "title": f"🎯 Nova Entrada — {emoji} {signal}",
        "description": f"Padrão detectado → entrada no **{signal}**",
        "color": color,
        "fields": [
            {"name": "Jogo", "value": game, "inline": True},
            {"name": "Entrada", "value": f"{emoji} {signal}", "inline": True},
            {"name": "Gale", "value": f"{gale_txt} ({gale}/{max_gale})", "inline": True},
            {"name": "Proteção Empate", "value": "Sim" if draw_prot else "Não", "inline": True},
        ],
        "timestamp": datetime.now(timezone.utc).isoformat(),
    }
    discord_send([embed])
    print(f"[DISCORD] Entrada enviada: {signal} {gale_txt}")


def discord_resultado_entrada(entrada: dict):
    signal = parse_signal(entrada.get("signal"))
    result = entrada.get("result", "?")
    gale = entrada.get("gale", 0)
    max_gale = entrada.get("maxGale", "?")
    emoji = SIGNAL_EMOJI.get(signal, "⚪")
    gale_txt = "Sem Gale" if gale == 0 else f"Gale {gale}"

    is_win = result == "WIN"
    color = 0x22C55E if is_win else 0xEF4444
    icon = "✅" if is_win else "❌"

    embed = {
        "title": f"{icon} {result} — {emoji} {signal} ({gale_txt})",
        "description": (
            f"Entrada no **{signal}** {'venceu' if is_win else 'perdeu'}!"
            + (f" (com Gale {gale})" if gale > 0 else " (sem Gale)")
        ),
        "color": color,
        "fields": [
            {"name": "Jogo", "value": GAME_NAMES.get(GAME_TYPE, "Bac Bo"), "inline": True},
            {"name": "Entrada", "value": f"{emoji} {signal}", "inline": True},
            {"name": "Resultado", "value": f"{icon} {result}", "inline": True},
            {"name": "Gale", "value": f"{gale}/{max_gale}", "inline": True},
        ],
        "timestamp": datetime.now(timezone.utc).isoformat(),
    }
    discord_send([embed])
    print(f"[DISCORD] Resultado: {result} | {signal} | {gale_txt}")


def discord_entrada_com_resultado(entrada: dict):
    signal = parse_signal(entrada.get("signal"))
    result = entrada.get("result", "?")
    gale = entrada.get("gale", 0)
    max_gale = entrada.get("maxGale", "?")
    emoji = SIGNAL_EMOJI.get(signal, "⚪")
    gale_txt = "Sem Gale" if gale == 0 else f"Gale {gale}"

    is_win = result == "WIN"
    color = 0x22C55E if is_win else 0xEF4444
    icon = "✅" if is_win else "❌"
    game = GAME_NAMES.get(GAME_TYPE, "Bac Bo")

    embed = {
        "title": f"🎯 {emoji} {signal} ({gale_txt}) → {icon} {result}",
        "description": (
            f"Entrada no **{signal}** resolveu rápido → **{result}**"
            + (f" (com Gale {gale})" if gale > 0 else "")
        ),
        "color": color,
        "fields": [
            {"name": "Jogo", "value": game, "inline": True},
            {"name": "Entrada", "value": f"{emoji} {signal}", "inline": True},
            {"name": "Resultado", "value": f"{icon} {result}", "inline": True},
            {"name": "Gale", "value": f"{gale}/{max_gale}", "inline": True},
        ],
        "timestamp": datetime.now(timezone.utc).isoformat(),
    }
    discord_send([embed])
    print(f"[DISCORD] Entrada+Resultado: {signal} {gale_txt} → {result}")


def discord_resultado_bacbo(data: dict):
    winner = data.get("winner", "?")
    score = data.get("Score", "?")
    hora = data.get("Hora", "?")
    emoji = SIGNAL_EMOJI.get(winner, "⚪")
    color = SIGNAL_COLOR.get(winner, 0x808080)

    embed = {
        "title": f"🎲 {emoji} {winner} — {score}",
        "color": color,
        "fields": [
            {"name": "Placar", "value": str(score), "inline": True},
            {"name": "Hora", "value": str(hora), "inline": True},
        ],
        "timestamp": datetime.now(timezone.utc).isoformat(),
    }
    discord_send([embed])


# ============================================================
# TRACKER DE ENTRADAS
# ============================================================

class EntryTracker:
    def __init__(self):
        self.pending_id = None
        self.pending_data = None
        self.last_entry_id = None
        self.known_entry_ids = set()
        self.notified_entry_ids = set()
        self.first_seen = {}
        self.first_load = True
        self._result_lock = threading.Lock()

    def _note_first_seen(self, eid, state, entry):
        """Registra a PRIMEIRA vez que vemos um ID e em que estado.

        state == 'PENDING'  → pegamos a entrada AO VIVO (bom).
        state == 'RESOLVED' → só vimos o registro já com resultado (ruim:
                              perdemos / o site não mandou a entrada ao vivo).
        """
        if eid in self.first_seen:
            return
        self.first_seen[eid] = state
        signal = parse_signal(entry.get("signal"))
        gale = entry.get("gale", 0)
        result = entry.get("result")
        if state == "PENDING":
            print(f"[DIAG] 1ª vez id={eid}: ✅ AO VIVO (pending) "
                  f"{signal} gale={gale}")
        else:
            print(f"[DIAG] 1ª vez id={eid}: ❌ JÁ RESOLVIDA "
                  f"{signal} gale={gale} result={result}  "
                  f"(entrada ao vivo NÃO foi vista!)")

    def process_pending(self, pending):
        if pending is None:
            if self.pending_id is not None:
                self.pending_id = None
                self.pending_data = None
            return

        pid = pending.get("id")
        if pid:
            self._note_first_seen(pid, "PENDING", pending)
        if pid and pid != self.pending_id:
            self.pending_id = pid
            self.pending_data = pending
            signal = parse_signal(pending.get("signal"))
            gale = pending.get("gale", 0)
            max_gale = pending.get("maxGale", "?")
            print(f"\n[ENTRADA] 🎯 Nova entrada: {signal} | "
                  f"Gale: {gale}/{max_gale}")
            discord_nova_entrada(pending)
            self.notified_entry_ids.add(pid)
            append_entrada(pending)

    def _send_resultado_delayed(self, entry, delay):
        def _send():
            time.sleep(delay)
            with self._result_lock:
                discord_resultado_entrada(entry)
        t = threading.Thread(target=_send, daemon=True)
        t.start()

    def process_entries(self, entries):
        if not entries:
            if self.first_load:
                self.first_load = False
                print("[TRACKER] Histórico vazio, pronto para receber entradas")
            return

        if self.first_load:
            for entry in entries:
                eid = entry.get("id")
                if eid:
                    self.known_entry_ids.add(eid)
                    # já conhecidas no arranque: não são "perdidas ao vivo"
                    self.first_seen[eid] = "STARTUP"
            if entries:
                top = entries[0].get("id")
                if top:
                    self.last_entry_id = top
            self.first_load = False
            print(f"[TRACKER] Histórico carregado: {len(entries)} entradas conhecidas")
            return

        for entry in reversed(entries):
            eid = entry.get("id")
            if not eid:
                continue

            result = entry.get("result")
            if eid not in self.known_entry_ids and result in ("WIN", "LOSS"):
                # Primeira vez que vemos esse ID e já vem resolvido →
                # a entrada ao vivo (pending) nunca foi capturada.
                self._note_first_seen(eid, "RESOLVED", entry)
                self.known_entry_ids.add(eid)
                signal = parse_signal(entry.get("signal"))
                gale = entry.get("gale", 0)
                gale_txt = "SG" if gale == 0 else f"G{gale}"
                icon = "✅" if result == "WIN" else "❌"
                print(f"[ENTRADA] 🎯 {signal} {gale_txt} → {icon} {result}")

                if eid in self.notified_entry_ids:
                    discord_resultado_entrada(entry)
                else:
                    discord_entrada_com_resultado(entry)
                append_entrada(entry)

                if eid == self.pending_id:
                    self.pending_id = None
                    self.pending_data = None

        if entries:
            top = entries[0].get("id")
            if top:
                self.last_entry_id = top

    def process_notify(self, notify_list):
        if not notify_list:
            return
        for n in notify_list:
            msg = n.get("message", "")
            if msg:
                print(f"[PADRÃO] {msg}")


# ============================================================
# BUSCAR HISTÓRICO COMPLETO VIA API
# ============================================================

def fetch_full_history(scraper, token):
    """Busca histórico de resultados (paginado) e entradas via REST."""
    h = {
        "Authorization": f"Bearer {token}",
        "Content-Type": "application/json",
        "Origin": ORIGIN,
        "Referer": f"{ORIGIN}/",
    }

    print("[HIST] Buscando histórico via API...")
    try:
        r = scraper.get(
            f"{API_BASE}/results?gameType={GAME_TYPE}&limit=1000&full=true",
            headers=h, timeout=20,
        )
        d = r.json()
    except Exception as e:
        print(f"[HIST] Erro na primeira requisição: {e}")
        return

    entries = d.get("historyEntries", [])
    all_results = d.get("data", [])
    total = d.get("totalResults", 0)
    has_more = d.get("hasMore", False)
    print(f"[HIST] Entradas do catalogador: {len(entries)} (historyLength: {d.get('historyLength')})")
    print(f"[HIST] Resultados Bac Bo: {len(all_results)}/{total} (página 1)")

    page = 2
    while has_more and len(all_results) < 5000:
        try:
            r = scraper.get(
                f"{API_BASE}/results?gameType={GAME_TYPE}&limit=1000&full=true&page={page}",
                headers=h, timeout=20,
            )
            d2 = r.json()
            batch = d2.get("data", [])
            if not batch:
                break
            all_results.extend(batch)
            has_more = d2.get("hasMore", False)
            print(f"[HIST] Página {page}: +{len(batch)} resultados ({len(all_results)} total)")
            page += 1
        except Exception as e:
            print(f"[HIST] Erro na página {page}: {e}")
            break

    if all_results:
        save_json(HISTORICO_RESULTADOS, all_results)
        print(f"[HIST] Salvo {len(all_results)} resultados em {HISTORICO_RESULTADOS}")

    if entries:
        save_entradas_bulk(entries)
        print(f"[HIST] Salvo {len(entries)} entradas em {HISTORICO_ENTRADAS}")

    return entries


# ============================================================
# POLLING VIA REST API (fallback quando Socket.IO é bloqueado)
# ============================================================

POLL_INTERVAL = 2

def poll_loop(scraper, auth, tracker):
    """Consulta a API REST a cada POLL_INTERVAL segundos."""
    print(f"[POLL] Modo polling ativo (a cada {POLL_INTERVAL}s)...")
    last_result_id = None

    while True:
        try:
            if not auth.access_token:
                auth.login()

            h = {
                "Authorization": f"Bearer {auth.access_token}",
                "Content-Type": "application/json",
                "Origin": ORIGIN,
                "Referer": f"{ORIGIN}/",
            }
            r = scraper.get(
                f"{API_BASE}/results?gameType={GAME_TYPE}&limit=20&full=true",
                headers=h, timeout=15,
            )

            if r.status_code == 401:
                print("[POLL] Token expirado, renovando...")
                auth.refresh()
                continue

            d = r.json()

            # Dump único da estrutura da resposta REST para confirmar se
            # existe algum campo de entrada pendente/ao vivo que devíamos ler.
            if not getattr(poll_loop, "_keys_dumped", False):
                poll_loop._keys_dumped = True
                if isinstance(d, dict):
                    print(f"[DIAG] Campos da resposta REST: {list(d.keys())}")
                    print(f"[DIAG] pendingEntry presente? "
                          f"{'sim' if d.get('pendingEntry') is not None else 'não/null'}")

            results = d.get("data", [])
            if results:
                newest = results[0]
                nid = newest.get("id")
                if nid and nid != last_result_id:
                    if last_result_id is not None:
                        winner = newest.get("winner", "?")
                        score = newest.get("Score", "?")
                        hora = newest.get("Hora", "?")
                        emoji = SIGNAL_EMOJI.get(winner, "?")
                        print(f"\n[BAC BO] {emoji} {winner} | {score} | {hora}")
                        append_resultado(newest)
                    last_result_id = nid

            entries = d.get("historyEntries", [])
            pending = d.get("pendingEntry")
            diag_snapshot("poll:rest", pending, entries)
            if entries:
                save_entradas_bulk(entries)
            tracker.process_entries(entries)
            tracker.process_pending(pending)

        except Exception as e:
            print(f"[POLL] Erro: {e}")

        time.sleep(POLL_INTERVAL)


# ============================================================
# SNIFFER: descobrir o endpoint da entrada AO VIVO
# ============================================================

# Chamado quando o site manda shouldRefresh=true. Testa endpoints
# candidatos e mostra qual existe e o que retorna, para acharmos onde
# fica a entrada pendente per-user.
PROBE_PATHS = [
    "/cataloguer?gameType={g}",
    "/cataloguer/current?gameType={g}",
    "/cataloguer/entries?gameType={g}",
    "/cataloguer/entry?gameType={g}",
    "/cataloguer/pending?gameType={g}",
    "/entries?gameType={g}",
    "/entries/pending?gameType={g}",
    "/entries/current?gameType={g}",
    "/user/entries?gameType={g}",
    "/user/cataloguer?gameType={g}",
    "/results/pending?gameType={g}",
    "/signals?gameType={g}",
    "/signals/current?gameType={g}",
]

_probe_count = 0
_PROBE_MAX = 6

def probe_endpoints(scraper, auth):
    """Testa endpoints candidatos (algumas vezes, para pegar um momento
    com entrada ativa) e loga status + estrutura da resposta."""
    global _probe_count
    if _probe_count >= _PROBE_MAX:
        return
    _probe_count += 1
    h = {
        "Authorization": f"Bearer {auth.access_token}",
        "Content-Type": "application/json",
        "Origin": ORIGIN,
        "Referer": f"{ORIGIN}/",
    }
    print(f"[PROBE] --- tentativa {_probe_count}/{_PROBE_MAX} ---")
    for tpl in PROBE_PATHS:
        path = tpl.format(g=GAME_TYPE)
        try:
            r = scraper.get(API_BASE + path, headers=h, timeout=10)
            status = r.status_code
            snippet = ""
            if status == 200:
                try:
                    j = r.json()
                    if isinstance(j, dict):
                        snippet = "dict keys=" + str(list(j.keys())[:12])
                    elif isinstance(j, list):
                        snippet = f"list[{len(j)}]"
                        if j:
                            snippet += " item0=" + str(j[0])[:120]
                    else:
                        snippet = str(j)[:120]
                except Exception:
                    snippet = "(não-JSON) " + r.text[:80]
            if status == 200:
                print(f"[PROBE] ✅ {status} {path}  {snippet}")
                if DIAG:
                    try:
                        with open(RAW_LOG, "a", encoding="utf-8") as f:
                            f.write(json.dumps({
                                "ts": datetime.now(timezone.utc).isoformat(),
                                "event": f"PROBE:{path}",
                                "status": status,
                                "body": r.text[:2000],
                            }, ensure_ascii=False) + "\n")
                    except Exception:
                        pass
            elif status not in (404, 400):
                print(f"[PROBE] ·  {status} {path}")
        except Exception as e:
            print(f"[PROBE] ERR {path}: {e}")


# ============================================================
# SOCKET.IO (modo preferido)
# ============================================================

def run_socket(scraper, auth, tracker, cf_cookies, cf_ua):
    if not cf_ua:
        cf_ua = scraper.headers.get("User-Agent") or (
            "Mozilla/5.0 (Windows NT 10.0; Win64; x64) "
            "AppleWebKit/537.36 (KHTML, like Gecko) "
            "Chrome/128.0.0.0 Safari/537.36")

    # IMPORTANTE: reutiliza a MESMA sessão do cloudscraper usada no /results.
    # Ela já tem o fingerprint de navegador + os cookies do Cloudflare que
    # fazem as chamadas REST passarem. Uma requests.Session() nova é
    # bloqueada com 403 pelo Cloudflare no handshake do Socket.IO.
    http_session = scraper
    http_session.headers.update({
        "User-Agent": cf_ua,
        "Origin": ORIGIN,
        "Referer": f"{ORIGIN}/",
    })
    for k, v in cf_cookies.items():
        try:
            http_session.cookies.set(k, v, domain=".historicbet.com")
        except Exception:
            pass

    sio = socketio.Client(
        reconnection=True,
        reconnection_attempts=0,
        reconnection_delay=2,
        reconnection_delay_max=30,
        logger=False,
        engineio_logger=False,
        http_session=http_session,
    )

    @sio.event
    def connect():
        print(f"[SOCKET] Conectado! Entrando: {GAME_NAMES.get(GAME_TYPE)}...")
        sio.emit("cataloguer:join", {"gameType": GAME_TYPE})

    @sio.event
    def disconnect():
        print("[SOCKET] Desconectado.")

    @sio.event
    def connect_error(data):
        print(f"[SOCKET] Erro: {data}")
        err = str(data)
        if "auth:" in err or "401" in err or "Token" in err:
            print("[SOCKET] Token expirado, renovando...")
            if auth.refresh():
                time.sleep(2)
                try:
                    connect_socket(sio, auth, cf_cookies, cf_ua)
                except Exception:
                    pass

    @sio.on("result:new")
    def on_new_result(data):
        winner = data.get("winner", "?")
        score = data.get("Score", "?")
        hora = data.get("Hora", "?")
        emoji = SIGNAL_EMOJI.get(winner, "?")
        print(f"\n[BAC BO] {emoji} {winner} | {score} | {hora}")
        append_resultado(data)

    @sio.on("result:meta")
    def on_meta(data):
        entries = data.get("historyEntries", [])
        pending = data.get("pendingEntry")
        notify = data.get("notify", [])
        length = data.get("historyLength", "?")
        print(f"[META] {length} resultados | {len(entries)} entradas")
        diag_raw("result:meta", data)
        # Dump único das chaves do meta, para achar o campo da entrada ao vivo.
        if not getattr(on_meta, "_keys_dumped", False):
            on_meta._keys_dumped = True
            if isinstance(data, dict):
                print(f"[DIAG] Chaves do result:meta: {list(data.keys())}")
        diag_snapshot("socket:meta", pending, entries)
        save_entradas_bulk(entries)
        tracker.process_entries(entries)
        tracker.process_pending(pending)
        tracker.process_notify(notify)

    @sio.on("result:user-update")
    def on_user_update(*args):
        print("[USER] Dados atualizados (shouldRefresh) → sondando endpoints...")
        # Suspeito principal da entrada ao vivo — grava o payload completo.
        diag_raw("result:user-update", list(args))
        # Descobrir ONDE fica a entrada ao vivo (per-user).
        try:
            probe_endpoints(scraper, auth)
        except Exception as e:
            print(f"[PROBE] Erro geral: {e}")

    @sio.on("result:winners")
    def on_winners(data):
        diag_raw("result:winners", data)

    @sio.on("result:stats-update")
    def on_stats(data):
        diag_raw("result:stats-update", data)

    @sio.on("deck:changed")
    def on_deck(data):
        hora = data.get("hora", "?")
        print(f"[DECK] Troca de baralho: {hora}")
        diag_raw("deck:changed", data)

    @sio.on("*")
    def catch_all(event, *args):
        # Qualquer evento SEM handler dedicado — pode ser aqui que a
        # entrada ao vivo é enviada (ex.: 'cataloguer:entry', 'signal:new').
        print(f"[EVENTO?] Evento não tratado: {event}")
        diag_raw(f"UNHANDLED:{event}", list(args))

    connect_socket(sio, auth, cf_cookies, cf_ua)

    try:
        sio.wait()
    except KeyboardInterrupt:
        print("\n[EXIT] Encerrando...")
        try:
            sio.emit("cataloguer:leave", {"gameType": GAME_TYPE})
            sio.disconnect()
        except Exception:
            pass


def connect_socket(sio, auth, cf_cookies, cf_ua):
    url = API_BASE
    path = "/socket.io"

    if API_BASE.endswith("/api"):
        url = API_BASE.rsplit("/api", 1)[0]
        path = "/api/socket.io"

    cookie_header = "; ".join(f"{k}={v}" for k, v in cf_cookies.items())

    headers = {
        "Origin": ORIGIN,
        "Referer": f"{ORIGIN}/",
        "User-Agent": cf_ua,
    }
    if cookie_header:
        headers["Cookie"] = cookie_header

    print(f"[SOCKET] Conectando a {url} (path: {path})...")

    # Começa em polling (usa o fingerprint do cloudscraper → passa pelo
    # Cloudflare como o /results). Se o upgrade para websocket funcionar,
    # o próprio Socket.IO faz sozinho depois do handshake.
    try:
        sio.connect(
            url,
            socketio_path=path,
            auth={"token": auth.access_token},
            transports=["polling", "websocket"],
            headers=headers,
            wait_timeout=20,
        )
    except Exception as e:
        print(f"[SOCKET] Handshake polling+websocket falhou: {e}")
        print("[SOCKET] Tentando somente websocket...")
        sio.connect(
            url,
            socketio_path=path,
            auth={"token": auth.access_token},
            transports=["websocket"],
            headers=headers,
            wait_timeout=20,
        )


# ============================================================
# ANÁLISE DO LOG DE DIAGNÓSTICO
# ============================================================

def analisar_diagnostico():
    """Lê o diagnostico_entradas.jsonl e responde a pergunta central:
    o site manda a entrada AO VIVO (pending) ou só vemos o resultado?"""
    if not os.path.exists(DIAG_LOG):
        print(f"[ANÁLISE] Arquivo {DIAG_LOG} não existe ainda.")
        print("Rode o bot normalmente por um tempo para gerar o log.")
        return

    seen_pending = {}      # id -> menor gale visto como pending
    seen_resolved = {}     # id -> (gale, result) da 1ª vez resolvido
    order_pending = {}     # id -> índice da linha em que virou pending
    order_resolved = {}    # id -> índice da linha em que apareceu resolvido

    linhas = 0
    with open(DIAG_LOG, "r", encoding="utf-8") as f:
        for idx, line in enumerate(f):
            line = line.strip()
            if not line:
                continue
            linhas += 1
            try:
                rec = json.loads(line)
            except Exception:
                continue

            p = rec.get("pending")
            if p and p.get("id") is not None:
                pid = p["id"]
                g = p.get("gale", 0) or 0
                if pid not in seen_pending or g < seen_pending[pid]:
                    seen_pending[pid] = g
                order_pending.setdefault(pid, idx)

            for e in rec.get("top_entries", []):
                if not e or e.get("id") is None:
                    continue
                if e.get("result") in ("WIN", "LOSS"):
                    eid = e["id"]
                    if eid not in seen_resolved:
                        seen_resolved[eid] = (e.get("gale", 0), e.get("result"))
                        order_resolved[eid] = idx

    ao_vivo = []       # apareceu como pending antes de resolver
    so_resultado = []  # só apareceu já resolvido, nunca como pending
    for eid in seen_resolved:
        if eid in seen_pending:
            ao_vivo.append(eid)
        else:
            so_resultado.append(eid)

    total = len(seen_resolved)
    perdeu_gale0 = [eid for eid in ao_vivo if seen_pending.get(eid, 0) > 0]

    print("=" * 55)
    print("  ANÁLISE DO DIAGNÓSTICO")
    print("=" * 55)
    print(f"  Snapshots lidos:              {linhas}")
    print(f"  Entradas resolvidas no log:   {total}")
    if total == 0:
        print("  (Nenhuma entrada resolvida capturada ainda.)")
        print("=" * 55)
        return
    print(f"  ✅ Vistas AO VIVO (pending):   {len(ao_vivo)}")
    print(f"  ❌ Só vistas JÁ RESOLVIDAS:    {len(so_resultado)}")
    print(f"     ↳ dessas ao vivo, começaram em gale>0: {len(perdeu_gale0)}")
    print("=" * 55)
    print("  VEREDITO:")
    if so_resultado and len(so_resultado) >= len(ao_vivo):
        print("  → A maioria das entradas NUNCA aparece como 'pending'.")
        print("    O bot só vê o resultado e reconstrói a 'entrada'.")
        print("    (Sua hipótese está correta.)")
    elif perdeu_gale0 and len(perdeu_gale0) >= max(1, len(ao_vivo) // 2):
        print("  → O site MANDA a entrada ao vivo, mas o bot frequentemente")
        print("    só a pega a partir do gale 1+ (perde o gale 0).")
    elif so_resultado:
        print("  → Na maioria o bot pega ao vivo, mas ainda perde algumas")
        print("    entradas (só vê o resultado).")
    else:
        print("  → O bot está pegando as entradas ao vivo corretamente.")
    print("=" * 55)


# ============================================================
# MAIN
# ============================================================

def run():
    cf_cookies, cf_ua, scraper = get_cf_cookies()

    auth = AuthManager(scraper)
    if not auth.login():
        sys.exit(1)

    initial_entries = fetch_full_history(scraper, auth.access_token)

    tracker = EntryTracker()
    if initial_entries is not None:
        for e in (initial_entries or []):
            eid = e.get("id")
            if eid:
                tracker.known_entry_ids.add(eid)
        if initial_entries:
            top = initial_entries[0].get("id")
            if top:
                tracker.last_entry_id = top
        tracker.first_load = False

    try:
        print("[MODE] Tentando Socket.IO...")
        run_socket(scraper, auth, tracker, cf_cookies, cf_ua)
    except Exception as e:
        print(f"[MODE] Socket.IO falhou: {e}")
        print("[MODE] Alternando para polling REST API...")
        poll_loop(scraper, auth, tracker)


if __name__ == "__main__":
    if len(sys.argv) > 1 and sys.argv[1] in ("--analisar", "--analise", "-a"):
        analisar_diagnostico()
        sys.exit(0)

    if _IMPORT_ERROR is not None:
        print(f"[ERRO] Falta uma dependência: {_IMPORT_ERROR}")
        print("Instale com: pip install python-socketio cloudscraper requests")
        sys.exit(1)

    print("=" * 55)
    print("  Historic BacBo → Discord Webhook")
    print("  Entradas + Resultados em tempo real")
    print("=" * 55)
    print(f"  Jogo:    {GAME_NAMES.get(GAME_TYPE, '?')}")
    print(f"  Hist. resultados: {HISTORICO_RESULTADOS}")
    print(f"  Hist. entradas:   {HISTORICO_ENTRADAS}")
    print("=" * 55)
    print()
    run()
