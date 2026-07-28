"""
Analisador da estratégia "LADO+N → TIE+N → aposta no oposto".

Regra:
  Quando um resultado é LADO (Player/Banker) com número N e o resultado
  SEGUINTE é TIE com o MESMO número N, aposta-se 1x no lado OPOSTO.
  O resultado logo depois decide:
    - veio o lado oposto  → WIN
    - veio o lado original → LOSS
    - veio Tie            → ANULADA (neutra, não conta)
  Sem gale, entrada única.

  Exemplo: Player 6 → Tie 6 → aposta Banker.

Uso:
  python3 analisar_tie.py            # roda no historico_resultados.json
  python3 analisar_tie.py --test     # valida a lógica nos exemplos
"""
import json
import sys
import os

RESULTS_FILE = "historico_resultados.json"

OPP = {"P": "B", "B": "P"}
NAME = {"P": "Player", "B": "Banker", "T": "Tie"}
EMOJI = {"P": "🔵", "B": "🔴", "T": "🟢"}


def side(r):
    w = str(r.get("winner", "")).strip().upper()
    if w.startswith("P"):
        return "P"
    if w.startswith("B"):
        return "B"
    if w.startswith("T"):
        return "T"
    return None


def score(r):
    v = r.get("Score", r.get("score"))
    if v is None:
        return None
    if isinstance(v, bool):
        return None
    if isinstance(v, (int, float)):
        return int(v)
    s = str(v).strip()
    try:
        return int(s)
    except ValueError:
        return s  # mantém como string se não for número puro


def load_chrono(path):
    with open(path, encoding="utf-8") as f:
        data = json.load(f)
    if not isinstance(data, list):
        raise SystemExit("Formato inesperado: esperado uma lista de resultados.")
    ids_ok = bool(data) and all(isinstance(r.get("id"), int) for r in data)
    if ids_ok:
        data = sorted(data, key=lambda r: r["id"])
        order = "ordenado por id crescente"
    else:
        data = list(reversed(data))
        order = "invertido (assumindo arquivo mais-novo-primeiro)"
    return data, order


def backtest(results):
    """Detecta o padrão LADO+N → TIE+N e resolve no resultado seguinte."""
    trig = []
    n = len(results)
    for i in range(n - 2):
        s1, n1 = side(results[i]), score(results[i])
        s2, n2 = side(results[i + 1]), score(results[i + 1])
        if s1 in ("P", "B") and s2 == "T" and n1 is not None and n1 == n2:
            bet = OPP[s1]
            s3 = side(results[i + 2])
            if s3 == "T":
                oc = "VOID"
            elif s3 is None:
                continue
            elif s3 == bet:
                oc = "WIN"
            else:
                oc = "LOSS"
            trig.append({"i": i, "lado": s1, "num": n1,
                         "bet": bet, "next": s3, "outcome": oc})
    return trig


def resumo(trig, titulo):
    w = sum(1 for t in trig if t["outcome"] == "WIN")
    l = sum(1 for t in trig if t["outcome"] == "LOSS")
    v = sum(1 for t in trig if t["outcome"] == "VOID")
    dec = w + l
    wr = (w / dec * 100) if dec else 0
    print(f"\n=== {titulo} ===")
    print(f"  Entradas:        {len(trig)}")
    print(f"  ✅ WIN:           {w}")
    print(f"  ❌ LOSS:          {l}")
    print(f"  ⚪ Anuladas:      {v}")
    print(f"  Taxa de acerto:  {wr:.1f}%   (ignora anuladas)")
    print(f"  Saldo (1:1):     {w - l:+d} unidades")


def analisar():
    if not os.path.exists(RESULTS_FILE):
        raise SystemExit(f"Não achei '{RESULTS_FILE}' na pasta atual.")
    results, order = load_chrono(RESULTS_FILE)

    print("=" * 58)
    print("  ESTRATÉGIA: LADO+N → TIE+N → aposta no oposto")
    print("=" * 58)
    print(f"  Resultados: {len(results)}  |  {order}")

    # Amostra crua para conferir o campo Score
    print("\n  Amostra crua (confira o campo 'Score'):")
    for r in results[:6]:
        print(f"    winner={r.get('winner')!r:>10}  Score={r.get('Score')!r:>6}  "
              f"id={r.get('id')}")

    # Funil: quão raro é o padrão
    total_tie = sum(1 for r in results if side(r) == "T")
    lado_tie = 0
    lado_tie_mesmo = 0
    for i in range(len(results) - 1):
        if side(results[i]) in ("P", "B") and side(results[i + 1]) == "T":
            lado_tie += 1
            n1, n2 = score(results[i]), score(results[i + 1])
            if n1 is not None and n1 == n2:
                lado_tie_mesmo += 1
    print("\n  Funil do padrão:")
    print(f"    Total de Ties:                        {total_tie}")
    print(f"    Casos 'LADO seguido de TIE':          {lado_tie}")
    print(f"    Desses, com MESMO número (gatilhos):  {lado_tie_mesmo}")

    trig = backtest(results)
    resumo(trig, "RESULTADO GERAL")

    if not trig:
        print("\n[AVISO] Nenhum gatilho encontrado. Se houver muitos Ties acima,")
        print("        o campo 'Score' pode ter outro formato — me manda a amostra crua.")
        return

    resumo([t for t in trig if t["lado"] == "P"],
           "Só Player+N → Tie+N (aposta Banker)")
    resumo([t for t in trig if t["lado"] == "B"],
           "Só Banker+N → Tie+N (aposta Player)")

    # Por número
    print("\n=== Por número (N) ===")
    print("  N  | entradas | WIN | LOSS | anul | taxa")
    by_n = {}
    for t in trig:
        by_n.setdefault(t["num"], []).append(t)
    for N in sorted(by_n, key=lambda x: (str(type(x)), x)):
        ts = by_n[N]
        w = sum(1 for t in ts if t["outcome"] == "WIN")
        l = sum(1 for t in ts if t["outcome"] == "LOSS")
        v = sum(1 for t in ts if t["outcome"] == "VOID")
        dec = w + l
        wr = (w / dec * 100) if dec else 0
        print(f"  {str(N):>2} | {len(ts):>8} | {w:>3} | {l:>4} | {v:>4} | {wr:5.1f}%")

    # Amostra
    print("\n=== Amostra (até 12 entradas) ===")
    for t in trig[:12]:
        print(f"  {EMOJI[t['lado']]} {NAME[t['lado']]} {t['num']} → 🟢 Tie {t['num']} "
              f"→ apostei {EMOJI[t['bet']]} {NAME[t['bet']]} "
              f"→ veio {NAME.get(t['next'], '?')} = {t['outcome']}")


def _selftest():
    casos = [
        # (nome, [(winner, Score), ...], esperado [outcomes])
        ("A", [("Player", 6), ("Tie", 6), ("Banker", 7)], ["WIN"]),
        ("B", [("Player", 6), ("Tie", 6), ("Player", 5)], ["LOSS"]),
        ("C", [("Player", 6), ("Tie", 6), ("Tie", 6)], ["VOID"]),
        ("D", [("Banker", 9), ("Tie", 9), ("Player", 4)], ["WIN"]),
        ("E", [("Player", 6), ("Tie", 8), ("Banker", 7)], []),   # número diferente
        ("F", [("Player", 6), ("Player", 6), ("Banker", 7)], []),  # 2º não é Tie
        ("G", [("Tie", 6), ("Tie", 6), ("Player", 7)], []),        # 1º não é lado
        ("H", [("Banker", 10), ("Tie", 10), ("Banker", 8)], ["LOSS"]),
    ]
    ok_all = True
    for nome, seq, esperado in casos:
        results = [{"winner": w, "Score": n, "id": i}
                   for i, (w, n) in enumerate(seq)]
        trig = backtest(results)
        got = [t["outcome"] for t in trig]
        ok = got == esperado
        ok_all = ok_all and ok
        print(f"  [{'OK ' if ok else 'FALHOU'}] Ex.{nome}: "
              f"esperado={esperado} obtido={got}")
    print("\nSelf-test:", "PASSOU ✅" if ok_all else "FALHOU ❌")


if __name__ == "__main__":
    if len(sys.argv) > 1 and sys.argv[1] == "--test":
        _selftest()
        sys.exit(0)
    analisar()
