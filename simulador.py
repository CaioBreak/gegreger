"""
Simulador (paper trading) da estratégia de reversão de sequência longa.

Estratégia:
  BASE: sequência de MIN_STREAK+ do mesmo lado (Tie é neutro/ignorado) →
        quando quebra (sai o oposto), aposta 1x que o PRÓXIMO resultado
        volta pro lado da sequência. Empate na aposta = ANULADA (neutra).
  META: só ENTRA de verdade (aposta dinheiro) depois que a BASE acumulou
        LOSS_THRESHOLD derrotas seguidas.

Banca fictícia de BANKROLL0, cada entrada aposta BET (pagamento 1:1).
Para quando a banca zera OU quando passam DAYS dias.
Manda no Discord a cada WIN e LOSS de uma entrada real (+ início e fim).

Começa a simular a partir do momento que você roda (estado fresco).
O estado é salvo em sim_estado.json — se cair/reiniciar, ele RETOMA de onde
parou. Use `--reset` para começar do zero.

Uso:
  python3 simulador.py            # roda (retoma se houver estado salvo)
  python3 simulador.py --reset    # zera e começa de novo
"""
import sys
import os
import time
import json
from datetime import datetime, timezone

import requests
import socketio

import historic_discord as h  # reaproveita login, cloudflare, constantes

# ============================================================
# CONFIGURAÇÃO
# ============================================================
WEBHOOK = ("https://discord.com/api/webhooks/1392694075588874240/"
           "PftVq_6TRJe5ZIFBV5ypVvX4FI4gSSFV4BqKvQ8PoIJK334Y_FqTqXwSkNE6TFP3DV5J")

MIN_STREAK = 6          # tamanho mínimo da sequência da base
LOSS_THRESHOLD = 2      # entra só após N derrotas seguidas da base
BANKROLL0 = 500.0       # banca inicial (R$)
BET = 100.0             # valor de cada entrada (R$)
DAYS = 7                # duração máxima da simulação

STATE_FILE = "sim_estado.json"

NAME = {"P": "Player", "B": "Banker", "T": "Tie"}
EMOJI = {"P": "🔵", "B": "🔴", "T": "🟢"}


def norm(side):
    w = ("" if side is None else str(side)).strip().upper()
    if w.startswith("P"):
        return "P"
    if w.startswith("B"):
        return "B"
    if w.startswith("T"):
        return "T"
    return None


def send_discord(embed):
    try:
        r = requests.post(WEBHOOK, json={"embeds": [embed]}, timeout=10)
        if r.status_code not in (200, 204):
            print(f"[DISCORD] status {r.status_code}: {r.text[:150]}")
    except Exception as e:
        print(f"[DISCORD] erro: {e}")


# ============================================================
# SIMULADOR
# ============================================================

class Sim:
    def __init__(self):
        self.start_ts = time.time()
        self.bankroll = BANKROLL0
        self.win = 0
        self.loss = 0
        self.void = 0
        self.base_signals = 0
        self.base_loss_streak = 0
        self.streak_side = None
        self.streak_len = 0
        self.pending = None
        self.last_id = None
        self.peak = BANKROLL0
        self.stopped = False
        self._sio = None

    # ---- persistência ----
    def to_dict(self):
        return {k: v for k, v in self.__dict__.items() if k != "_sio"}

    @classmethod
    def from_dict(cls, d):
        s = cls()
        for k, v in d.items():
            setattr(s, k, v)
        return s

    def save(self):
        try:
            with open(STATE_FILE, "w", encoding="utf-8") as f:
                json.dump(self.to_dict(), f, ensure_ascii=False, indent=2)
        except Exception as e:
            print(f"[ESTADO] erro ao salvar: {e}")

    # ---- lógica principal ----
    def on_result(self, s, rid):
        if self.stopped:
            return
        if rid is not None and rid == self.last_id:
            return  # duplicado (reconexão)
        self.last_id = rid

        # 1) resolve a aposta pendente da base
        if self.pending is not None:
            bet = self.pending["bet"]
            if s == "T":
                outcome = "VOID"
            elif s == bet:
                outcome = "WIN"
            else:
                outcome = "LOSS"
            self._resolve(outcome)

        # 2) atualiza a sequência (para gerar sinais futuros)
        if s != "T":
            if s == self.streak_side:
                self.streak_len += 1
            else:
                if self.streak_side is not None and self.streak_len >= MIN_STREAK:
                    is_real = self.base_loss_streak >= LOSS_THRESHOLD
                    self.pending = {
                        "bet": self.streak_side,
                        "streak_side": self.streak_side,
                        "streak_len": self.streak_len,
                        "break": s,
                        "is_real": is_real,
                    }
                    self.base_signals += 1
                    tag = "ENTRADA REAL 💰" if is_real else "só observando"
                    print(f"[BASE] sinal #{self.base_signals}: "
                          f"{self.streak_len}x {NAME[self.streak_side]} quebrou em "
                          f"{NAME[s]} → aposta {NAME[self.streak_side]} "
                          f"| {tag} (derrotas base seguidas={self.base_loss_streak})")
                self.streak_side = s
                self.streak_len = 1

        self.save()
        if time.time() - self.start_ts >= DAYS * 86400:
            self.stop("🏁 Semana concluída")

    def _resolve(self, outcome):
        p = self.pending
        self.pending = None
        if outcome == "VOID":
            self.void += 1
            print("[BASE] resultado ANULADO (empate) — neutro, não conta")
            return
        real = p["is_real"]
        if real:
            if outcome == "WIN":
                self.bankroll += BET
                self.win += 1
                self.peak = max(self.peak, self.bankroll)
                self._webhook_entry("WIN", p)
            else:
                self.bankroll -= BET
                self.loss += 1
                self._webhook_entry("LOSS", p)
        else:
            print(f"[BASE] {outcome} (não era entrada real ainda)")
        self.base_loss_streak = self.base_loss_streak + 1 if outcome == "LOSS" else 0
        if real and self.bankroll <= 0:
            self.stop("💀 Banca zerada")

    def _webhook_entry(self, outcome, p):
        win = outcome == "WIN"
        color = 0x22C55E if win else 0xEF4444
        icon = "✅" if win else "❌"
        delta = f"+R$ {BET:.0f}" if win else f"-R$ {BET:.0f}"
        side = NAME[p["bet"]]
        emoji = EMOJI[p["bet"]]
        total = self.win + self.loss
        wr = (self.win / total * 100) if total else 0
        embed = {
            "title": f"{icon} {outcome} — apostou {emoji} {side}",
            "color": color,
            "fields": [
                {"name": "Resultado", "value": f"{icon} {outcome} ({delta})", "inline": True},
                {"name": "Aposta", "value": f"{emoji} {side} — R$ {BET:.0f}", "inline": True},
                {"name": "Gatilho", "value": f"{p['streak_len']}x {NAME[p['streak_side']]} → quebra", "inline": True},
                {"name": "Banca", "value": f"R$ {self.bankroll:.0f}", "inline": True},
                {"name": "Placar", "value": f"{self.win}W / {self.loss}L ({wr:.0f}%)", "inline": True},
                {"name": "Saldo", "value": f"R$ {self.bankroll - BANKROLL0:+.0f}", "inline": True},
            ],
            "timestamp": datetime.now(timezone.utc).isoformat(),
        }
        send_discord(embed)
        print(f"[ENTRADA] {outcome} | aposta {side} | banca R$ {self.bankroll:.0f} "
              f"| {self.win}W/{self.loss}L")

    def stop(self, motivo):
        if self.stopped:
            return
        self.stopped = True
        self.save()
        total = self.win + self.loss
        wr = (self.win / total * 100) if total else 0
        horas = (time.time() - self.start_ts) / 3600
        embed = {
            "title": f"{motivo} — Simulação encerrada",
            "color": 0x3B82F6,
            "fields": [
                {"name": "Banca final", "value": f"R$ {self.bankroll:.0f}", "inline": True},
                {"name": "Saldo", "value": f"R$ {self.bankroll - BANKROLL0:+.0f}", "inline": True},
                {"name": "Pico", "value": f"R$ {self.peak:.0f}", "inline": True},
                {"name": "Entradas", "value": f"{total} ({self.win}W/{self.loss}L, {wr:.0f}%)", "inline": True},
                {"name": "Anuladas", "value": f"{self.void}", "inline": True},
                {"name": "Tempo", "value": f"{horas:.1f}h", "inline": True},
            ],
            "timestamp": datetime.now(timezone.utc).isoformat(),
        }
        send_discord(embed)
        print(f"[FIM] {motivo} | banca R$ {self.bankroll:.0f} | {self.win}W/{self.loss}L")
        if self._sio is not None:
            try:
                self._sio.disconnect()
            except Exception:
                pass


def embed_inicio():
    return {
        "title": "🎬 Simulação iniciada (paper trading)",
        "color": 0x3B82F6,
        "description": (
            f"**Estratégia:** base **{MIN_STREAK}+** no mesmo lado → aposta na reversão, "
            f"entrando só **após {LOSS_THRESHOLD} derrotas seguidas** da base.\n"
            f"**Banca:** R$ {BANKROLL0:.0f}  |  **Aposta:** R$ {BET:.0f}  |  "
            f"**Duração:** {DAYS} dias (ou até zerar).\n"
            f"_Começando a observar a partir de agora — precisa ver uma sequência de "
            f"{MIN_STREAK}+ ao vivo antes do primeiro sinal._"
        ),
        "timestamp": datetime.now(timezone.utc).isoformat(),
    }


def carregar_ou_novo():
    """Retorna (sim, is_novo)."""
    if "--reset" not in sys.argv and os.path.exists(STATE_FILE):
        try:
            d = json.load(open(STATE_FILE, encoding="utf-8"))
            sim = Sim.from_dict(d)
            if sim.stopped:
                print("[ESTADO] Simulação anterior já encerrada. "
                      "Use --reset para começar de novo.")
                sys.exit(0)
            print(f"[ESTADO] Retomando: banca R$ {sim.bankroll:.0f}, "
                  f"{sim.win}W/{sim.loss}L, rodando há "
                  f"{(time.time() - sim.start_ts) / 3600:.1f}h")
            return sim, False
        except SystemExit:
            raise
        except Exception as e:
            print(f"[ESTADO] Falha ao carregar ({e}) — começando novo.")
    return Sim(), True


# ============================================================
# CONEXÃO AO VIVO
# ============================================================

def main():
    print("=" * 55)
    print("  SIMULADOR — estratégia reversão + meta (paper trading)")
    print(f"  Banca R$ {BANKROLL0:.0f} | Aposta R$ {BET:.0f} | "
          f"base {MIN_STREAK}+ | após {LOSS_THRESHOLD} derrotas | {DAYS} dias")
    print("=" * 55)

    sim, novo = carregar_ou_novo()
    if novo:
        send_discord(embed_inicio())

    cf, ua, scraper = h.get_cf_cookies()
    auth = h.AuthManager(scraper)
    if not auth.login():
        raise SystemExit("[AUTH] login falhou")

    cf_ua = ua or scraper.headers.get("User-Agent") or ""
    http_session = scraper
    http_session.headers.update({
        "User-Agent": cf_ua, "Origin": h.ORIGIN, "Referer": h.ORIGIN + "/",
    })
    for k, v in (cf or {}).items():
        try:
            http_session.cookies.set(k, v, domain=".historicbet.com")
        except Exception:
            pass

    sio = socketio.Client(reconnection=True, reconnection_attempts=0,
                          reconnection_delay=2, reconnection_delay_max=30,
                          logger=False, engineio_logger=False,
                          http_session=http_session)
    sim._sio = sio

    @sio.event
    def connect():
        print(f"[SOCKET] conectado! entrando em {h.GAME_NAMES.get(h.GAME_TYPE)}...")
        sio.emit("cataloguer:join", {"gameType": h.GAME_TYPE})

    @sio.event
    def disconnect():
        print("[SOCKET] desconectado (tentando reconectar se possível)")

    @sio.on("result:new")
    def on_new(data):
        s = norm(data.get("winner"))
        if s is None:
            return
        rid = data.get("id")
        hora = data.get("Hora", "?")
        # sequência JÁ incluindo este resultado (para o log não confundir)
        if s == "T":
            base = f"{sim.streak_len}x {NAME[sim.streak_side]}" if sim.streak_side else "-"
            seq = f"{base} (empate: neutro)"
        elif s == sim.streak_side:
            seq = f"{sim.streak_len + 1}x {NAME[s]}"
        else:
            seq = f"1x {NAME[s]}"
        print(f"[BAC BO] {EMOJI[s]} {NAME[s]} | {hora} | sequência: {seq}")
        sim.on_result(s, rid)
        if sim.stopped:
            try:
                sio.disconnect()
            except Exception:
                pass

    h.connect_socket(sio, auth, cf, cf_ua)
    try:
        sio.wait()
    except KeyboardInterrupt:
        print("\n[EXIT] interrompido pelo usuário")
        sim.stop("⏹️ Interrompido manualmente")
        try:
            sio.disconnect()
        except Exception:
            pass


if __name__ == "__main__":
    main()
