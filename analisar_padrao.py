"""
Analisador do padrão "reversão de sequência longa" no histórico do Bac Bo.

Regra (definida pelo usuário):
  - Sequência de N+ resultados do MESMO lado (Player ou Banker), N >= 6.
  - Empate (Tie) é NEUTRO: não conta e não quebra a sequência (é ignorado).
  - Quando sai 1 resultado do lado OPOSTO (quebra), aposta-se UMA vez que o
    PRÓXIMO resultado volta para o lado que estava emendando.
  - SEM gale: entrada única.
  - Se o próximo resultado for Empate → entrada ANULADA (não ganha nem perde,
    e NÃO segue para nova tentativa).

Uso:
  python3 analisar_padrao.py            # limiar padrão = 6
  python3 analisar_padrao.py 8          # testa limiar = 8
  python3 analisar_padrao.py --test     # roda os exemplos de validação
"""
import json
import sys
import os

RESULTS_FILE = "historico_resultados.json"

NAME = {"P": "Player", "B": "Banker", "T": "Tie"}


def norm_side(r):
    """Normaliza o vencedor de um resultado para 'P', 'B', 'T' ou None."""
    w = r.get("winner")
    if w is None:
        for k in ("Winner", "result", "side", "color", "vencedor"):
            if r.get(k) is not None:
                w = r.get(k)
                break
    w = ("" if w is None else str(w)).strip().upper()
    if w.startswith("P"):
        return "P"
    if w.startswith("B"):
        return "B"
    if w.startswith("T"):
        return "T"
    return None


def load_chrono(path):
    """Carrega os resultados em ordem CRONOLÓGICA (mais antigo → mais novo)."""
    with open(path, encoding="utf-8") as f:
        data = json.load(f)
    if not isinstance(data, list):
        raise SystemExit("Formato inesperado: esperado uma lista de resultados.")
    ids_ok = bool(data) and all(isinstance(r.get("id"), int) for r in data)
    if ids_ok:
        data = sorted(data, key=lambda r: r["id"])
        order = "ordenado por id crescente"
    else:
        # o arquivo é salvo com o mais novo primeiro → invertemos
        data = list(reversed(data))
        order = "invertido (assumindo arquivo mais-novo-primeiro)"
    return data, order


def backtest(results, min_streak):
    """Percorre os resultados e devolve a lista de entradas disparadas."""
    streak_side = None
    streak_len = 0
    triggers = []
    n = len(results)
    for i in range(n):
        side = norm_side(results[i])
        if side is None or side == "T":
            continue  # inválido ou empate → neutro, ignora
        if side == streak_side:
            streak_len += 1
        else:
            # trocou de lado = QUEBRA da sequência anterior
            if streak_side is not None and streak_len >= min_streak:
                bet = streak_side
                if i + 1 < n:
                    nxt = norm_side(results[i + 1])
                    if nxt == "T":
                        outcome = "VOID"
                    elif nxt == bet:
                        outcome = "WIN"
                    elif nxt is None:
                        outcome = None  # sem dado → descarta
                    else:
                        outcome = "LOSS"
                    if outcome is not None:
                        triggers.append({
                            "streak_side": streak_side,
                            "streak_len": streak_len,
                            "bet": bet,
                            "break": side,
                            "next": nxt,
                            "outcome": outcome,
                            "index": i,
                        })
            streak_side = side
            streak_len = 1
    return triggers


def resumo(triggers, titulo):
    wins = sum(1 for t in triggers if t["outcome"] == "WIN")
    losses = sum(1 for t in triggers if t["outcome"] == "LOSS")
    voids = sum(1 for t in triggers if t["outcome"] == "VOID")
    total = len(triggers)
    decididas = wins + losses
    wr = (wins / decididas * 100) if decididas else 0.0
    net = wins - losses
    print(f"\n=== {titulo} ===")
    print(f"  Entradas (gatilhos):   {total}")
    print(f"  ✅ WIN:                 {wins}")
    print(f"  ❌ LOSS:                {losses}")
    print(f"  ⚪ Anuladas (empate):   {voids}")
    print(f"  Taxa de acerto:        {wr:.1f}%   (WIN / (WIN+LOSS), ignora anuladas)")
    print(f"  Saldo (pagto 1:1):     {net:+d} unidades")


def analisar(min_streak):
    if not os.path.exists(RESULTS_FILE):
        raise SystemExit(f"Não achei '{RESULTS_FILE}' na pasta atual.")
    results, order = load_chrono(RESULTS_FILE)

    dist = {"P": 0, "B": 0, "T": 0, None: 0}
    for r in results:
        dist[norm_side(r)] = dist.get(norm_side(r), 0) + 1
    validos = dist["P"] + dist["B"] + dist["T"]

    print("=" * 55)
    print("  ANÁLISE DO PADRÃO — reversão de sequência longa")
    print("=" * 55)
    print(f"  Arquivo:       {RESULTS_FILE}")
    print(f"  Resultados:    {len(results)}  (válidos: {validos})")
    print(f"  Ordem:         {order}")
    print(f"  Distribuição:  Player={dist['P']}  Banker={dist['B']}  "
          f"Tie={dist['T']}  inválidos={dist[None]}")

    if validos < 50:
        print("\n[AVISO] Poucos resultados válidos — confira o campo 'winner'.")
        return

    trig = backtest(results, min_streak)
    resumo(trig, f"LIMIAR = {min_streak}+ sequências (todos os lados)")
    resumo([t for t in trig if t["streak_side"] == "P"],
           "Só sequências de Player (aposta Player)")
    resumo([t for t in trig if t["streak_side"] == "B"],
           "Só sequências de Banker (aposta Banker)")

    # Por comprimento exato da sequência
    print("\n=== Por comprimento da sequência ===")
    print("  tam | entradas | WIN | LOSS | anul | taxa")
    by_len = {}
    for t in trig:
        by_len.setdefault(t["streak_len"], []).append(t)
    for L in sorted(by_len):
        ts = by_len[L]
        w = sum(1 for t in ts if t["outcome"] == "WIN")
        l = sum(1 for t in ts if t["outcome"] == "LOSS")
        v = sum(1 for t in ts if t["outcome"] == "VOID")
        dec = w + l
        wr = (w / dec * 100) if dec else 0
        print(f"  {L:>3} | {len(ts):>8} | {w:>3} | {l:>4} | {v:>4} | {wr:5.1f}%")

    # Varredura de limiares
    print("\n=== Varredura de limiares (6 a 12) ===")
    print("  limiar | entradas | WIN | LOSS | anul | taxa  | saldo")
    for N in range(6, 13):
        ts = backtest(results, N)
        w = sum(1 for t in ts if t["outcome"] == "WIN")
        l = sum(1 for t in ts if t["outcome"] == "LOSS")
        v = sum(1 for t in ts if t["outcome"] == "VOID")
        dec = w + l
        wr = (w / dec * 100) if dec else 0
        print(f"  {N:>6} | {len(ts):>8} | {w:>3} | {l:>4} | {v:>4} | "
              f"{wr:5.1f}% | {w - l:+d}")

    # Amostra de entradas
    print("\n=== Amostra (primeiras 8 entradas) ===")
    for t in trig[:8]:
        print(f"  seq {t['streak_len']}x {NAME[t['streak_side']]:6} "
              f"→ quebrou em {NAME[t['break']]:6} "
              f"→ apostei {NAME[t['bet']]:6} "
              f"→ veio {NAME.get(t['next'], '?'):6} = {t['outcome']}")


def _selftest():
    casos = [
        ("A", ["B", "B", "B", "B", "B", "B", "P", "B"], [("B", 6, "WIN")]),
        ("B", ["P", "P", "P", "T", "P", "P", "P", "B", "P"], [("P", 6, "WIN")]),
        ("C", ["B", "B", "B", "B", "B", "B", "B", "P", "P", "B"], [("B", 7, "LOSS")]),
        ("D", ["P", "P", "P", "P", "P", "B"], []),
        ("E", ["B", "B", "B", "B", "B", "B", "T", "B"], []),
        ("F", ["P", "P", "P", "P", "P", "P", "B", "T", "P"], [("P", 6, "VOID")]),
    ]
    todos_ok = True
    for nome, seq, esperado in casos:
        results = [{"winner": NAME[s], "id": i} for i, s in enumerate(seq)]
        trig = backtest(results, 6)
        got = [(t["streak_side"], t["streak_len"], t["outcome"]) for t in trig]
        ok = got == esperado
        todos_ok = todos_ok and ok
        print(f"  [{'OK ' if ok else 'FALHOU'}] Ex.{nome}: "
              f"esperado={esperado} obtido={got}")
    print("\nSelf-test:", "PASSOU ✅" if todos_ok else "FALHOU ❌")


if __name__ == "__main__":
    if len(sys.argv) > 1 and sys.argv[1] == "--test":
        _selftest()
        sys.exit(0)
    ms = 6
    if len(sys.argv) > 1:
        try:
            ms = int(sys.argv[1])
        except ValueError:
            pass
    analisar(ms)
