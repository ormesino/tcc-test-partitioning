#!/usr/bin/env python3
"""
generate_charts.py — Gera gráficos acadêmicos a partir dos resultados
do benchmark de particionamento de testes.

Uso:
    python scripts/generate_charts.py <run_dir>

Exemplo:
    python scripts/generate_charts.py benchmarks/results/cli/20260609-113627

O script lê raw.csv, aggregate.csv e config.json do diretório informado
e gera gráficos na subpasta charts/ dentro do mesmo diretório.

Gráficos gerados:
    1. makespan_boxplot.png    — Boxplot de makespan real por algoritmo × workers
    2. makespan_bars.png       — Barras agrupadas de makespan mediano
    3. speedup.png             — Speedup vs. workers (T1 medido + T1 teórico)
    4. planned_vs_actual.png   — Makespan planejado (teórico) vs. real
    5. load_stddev.png         — Desvio padrão da carga entre workers
    6. resumo.png              — Painel 2×2 com os gráficos mais relevantes

Decisões metodológicas incorporadas:
    - Decisão 1: Speedup calculado com dois T1 (medido e teórico).
    - Decisão 2: Boxplots com 3 repetições, mediana como tendência central.
    - Decisão 4: p=8 mantido para mostrar saturação.
"""

import sys
import os
import json

import pandas as pd
import matplotlib
matplotlib.use("Agg")
import matplotlib.pyplot as plt
import matplotlib.ticker as ticker

# ---------- Paleta e estilo ------------------------------------------------

ALGO_COLORS = {
    "Round-Robin":  "#E74C3C",   # vermelho
    "Quantity":     "#3498DB",   # azul
    "LPT":         "#2ECC71",   # verde
    "FFD-Multifit": "#9B59B6",  # roxo
}

ALGO_ORDER = ["Round-Robin", "Quantity", "LPT", "FFD-Multifit"]

ALGO_SHORT = {
    "Round-Robin":  "RR",
    "Quantity":     "QTY",
    "LPT":         "LPT",
    "FFD-Multifit": "FFD",
}

plt.rcParams.update({
    "figure.dpi": 150,
    "savefig.dpi": 150,
    "font.size": 11,
    "axes.titlesize": 13,
    "axes.labelsize": 12,
    "legend.fontsize": 10,
    "xtick.labelsize": 10,
    "ytick.labelsize": 10,
    "figure.facecolor": "white",
    "axes.facecolor": "#FAFAFA",
    "axes.grid": True,
    "grid.alpha": 0.3,
    "grid.linestyle": "--",
})


# ---------- Funções auxiliares ----------------------------------------------

def ns_to_seconds(ns):
    """Converte nanosegundos para segundos."""
    return ns / 1e9


def format_seconds(s):
    """Formata segundos em mm:ss ou s."""
    if s >= 60:
        m = int(s // 60)
        sec = s % 60
        return f"{m}m{sec:.0f}s"
    return f"{s:.1f}s"


def load_t1_measured(config):
    """Carrega T1 do baseline-seq JSON."""
    baseline_path = config["projects"][0].get("baseline_seq_file", "")
    if not baseline_path or not os.path.exists(baseline_path):
        return None
    with open(baseline_path) as f:
        data = json.load(f)
    return data["duration_ns"] / 1e9


def load_t1_theoretical(config):
    """Calcula T1 teórico = sum(Duration) do JSON de caracterização."""
    data_path = config["projects"][0].get("data_file", "")
    if not data_path or not os.path.exists(data_path):
        return None
    with open(data_path) as f:
        packages = json.load(f)
    return sum(p["duration_ns"] for p in packages) / 1e9


def get_project_name(config):
    """Retorna o nome do projeto."""
    return config["projects"][0]["name"]


# ---------- Gráfico 1: Boxplot de Makespan ----------------------------------

def plot_makespan_boxplot(raw, charts_dir, project_name):
    """Boxplot de makespan real por algoritmo, facetado por workers."""
    workers_list = sorted(raw["workers"].unique())
    n_workers = len(workers_list)

    fig, axes = plt.subplots(1, n_workers, figsize=(5 * n_workers, 5),
                             sharey=True)
    if n_workers == 1:
        axes = [axes]

    for ax, w in zip(axes, workers_list):
        data_by_algo = []
        labels = []
        colors = []

        for algo in ALGO_ORDER:
            subset = raw[(raw["algorithm"] == algo) & (raw["workers"] == w)]
            if subset.empty:
                continue
            vals = ns_to_seconds(subset["exec_makespan_ns"].dropna())
            data_by_algo.append(vals.values)
            labels.append(ALGO_SHORT[algo])
            colors.append(ALGO_COLORS[algo])

        bp = ax.boxplot(data_by_algo, tick_labels=labels, patch_artist=True,
                        widths=0.6, medianprops=dict(color="black", linewidth=2))

        for patch, color in zip(bp["boxes"], colors):
            patch.set_facecolor(color)
            patch.set_alpha(0.7)

        ax.set_title(f"p = {w}")
        ax.set_xlabel("Algoritmo")
        ax.yaxis.set_major_formatter(
            ticker.FuncFormatter(lambda x, _: format_seconds(x)))

    axes[0].set_ylabel("Makespan Real (s)")
    fig.suptitle(f"Distribuição do Makespan — {project_name}",
                 fontsize=14, fontweight="bold", y=1.02)
    fig.tight_layout()
    fig.savefig(os.path.join(charts_dir, "makespan_boxplot.png"),
                bbox_inches="tight")
    plt.close(fig)
    print("  [OK] makespan_boxplot.png")


# ---------- Gráfico 2: Barras de Makespan (mediana) -------------------------

def plot_makespan_bars(agg, native, charts_dir, project_name):
    """Barras agrupadas de makespan mediano por workers × algoritmo."""
    workers_list = sorted(agg["workers"].unique())
    n_algos = len(ALGO_ORDER)
    x = range(len(workers_list))
    width = 0.8 / n_algos

    fig, ax = plt.subplots(figsize=(8, 5))

    for i, algo in enumerate(ALGO_ORDER):
        subset = agg[agg["algorithm"] == algo].sort_values("workers")
        vals = ns_to_seconds(subset["exec_makespan_median_ns"].values)
        positions = [xi + i * width for xi in x]
        bars = ax.bar(positions, vals, width, label=algo,
                      color=ALGO_COLORS[algo], alpha=0.85, edgecolor="white")

        for bar, val in zip(bars, vals):
            ax.text(bar.get_x() + bar.get_width() / 2, bar.get_height() + 1,
                    format_seconds(val), ha="center", va="bottom", fontsize=8)

    if native is not None and not native.empty:
        native = native.sort_values("workers")
        ax.plot([xi + width * (n_algos - 1) / 2 for xi in x],
                ns_to_seconds(native["duration_ns"]), "kD--",
                label="Go Native", linewidth=1.5, markersize=6)

    ax.set_xlabel("Número de Workers (p)")
    ax.set_ylabel("Makespan Mediano (s)")
    ax.set_title(f"Makespan Real por Algoritmo — {project_name}",
                 fontweight="bold")
    ax.set_xticks([xi + width * (n_algos - 1) / 2 for xi in x])
    ax.set_xticklabels([f"p={w}" for w in workers_list])
    ax.legend(loc="upper right")
    ax.yaxis.set_major_formatter(
        ticker.FuncFormatter(lambda x, _: format_seconds(x)))

    fig.tight_layout()
    fig.savefig(os.path.join(charts_dir, "makespan_bars.png"),
                bbox_inches="tight")
    plt.close(fig)
    print("  [OK] makespan_bars.png")


# ---------- Gráfico 3: Speedup ---------------------------------------------

def plot_speedup(raw, agg, native, t1_measured, charts_dir, project_name):
    """Speedup empírico e planejado, sem misturar wall-clock e simulação."""
    workers_list = sorted(agg["workers"].unique())

    fig, axes = plt.subplots(1, 2, figsize=(12, 5), sharey=False)

    # Painel esquerdo: T1 medido (baseline-seq)
    ax = axes[0]
    if t1_measured:
        for algo in ALGO_ORDER:
            subset = agg[agg["algorithm"] == algo].sort_values("workers")
            makespans = ns_to_seconds(subset["exec_makespan_median_ns"].values)
            speedups = [t1_measured / m for m in makespans]
            ax.plot(workers_list, speedups, "o-", label=algo,
                    color=ALGO_COLORS[algo], linewidth=2, markersize=6)

        ax.plot(workers_list, workers_list, "k--", alpha=0.4,
                label="Ideal (linear)")
        if native is not None and not native.empty:
            native = native.sort_values("workers")
            ax.plot(native["workers"], native["speedup"], "kD-.",
                    label="Go Native", linewidth=1.5, markersize=6)
        ax.set_title("Speedup com T1 Medido\n(baseline-seq, inclui overhead)")
    else:
        ax.text(0.5, 0.5, "T1 medido não disponível",
                transform=ax.transAxes, ha="center")
        ax.set_title("Speedup com T1 Medido")

    ax.set_xlabel("Workers (p)")
    ax.set_ylabel("Speedup S(p) = T1 / Tp")
    ax.legend(fontsize=9)
    ax.set_xticks(workers_list)

    # Painel direito: T1 teórico / makespan planejado, ambos simulados.
    ax = axes[1]
    for algo in ALGO_ORDER:
        subset = (raw[raw["algorithm"] == algo]
                  .groupby("workers", as_index=False)["planned_speedup"].median()
                  .sort_values("workers"))
        ax.plot(subset["workers"], subset["planned_speedup"], "o-", label=algo,
                color=ALGO_COLORS[algo], linewidth=2, markersize=6)

    ax.plot(workers_list, workers_list, "k--", alpha=0.4,
            label="Ideal (linear)")
    ax.set_title("Speedup Planejado\n(T1 e Tp caracterizados)")

    ax.set_xlabel("Workers (p)")
    ax.set_ylabel("Speedup S(p) = T1 / Tp")
    ax.legend(fontsize=9)
    ax.set_xticks(workers_list)

    fig.suptitle(f"Análise de Speedup — {project_name}",
                 fontsize=14, fontweight="bold", y=1.02)
    fig.tight_layout()
    fig.savefig(os.path.join(charts_dir, "speedup.png"),
                bbox_inches="tight")
    plt.close(fig)
    print("  [OK] speedup.png")


# ---------- Gráfico 4: Planejado vs. Real ----------------------------------

def plot_planned_vs_actual(raw, charts_dir, project_name):
    """Scatter: makespan planejado (simulate) vs. makespan real."""
    workers_list = sorted(raw["workers"].unique())
    n_workers = len(workers_list)

    fig, axes = plt.subplots(1, n_workers, figsize=(5 * n_workers, 5))
    if n_workers == 1:
        axes = [axes]

    for ax, w in zip(axes, workers_list):
        subset = raw[raw["workers"] == w]

        for algo in ALGO_ORDER:
            data = subset[subset["algorithm"] == algo]
            if data.empty:
                continue
            planned = ns_to_seconds(data["planned_makespan_ns"])
            actual = ns_to_seconds(data["exec_makespan_ns"].dropna())
            if len(planned) != len(actual):
                continue

            ax.scatter(planned, actual, label=algo, color=ALGO_COLORS[algo],
                       s=40, alpha=0.8, edgecolors="white", linewidth=0.5)

        # Linha de referência y = x
        all_vals = ns_to_seconds(
            subset[["planned_makespan_ns", "exec_makespan_ns"]].dropna().values.flatten())
        if len(all_vals) > 0:
            lim_min = min(all_vals) * 0.8
            lim_max = max(all_vals) * 1.1
            ax.plot([lim_min, lim_max], [lim_min, lim_max], "k--", alpha=0.3)

        ax.set_title(f"p = {w}")
        ax.set_xlabel("Makespan Planejado (s)")
        ax.xaxis.set_major_formatter(
            ticker.FuncFormatter(lambda x, _: format_seconds(x)))
        ax.yaxis.set_major_formatter(
            ticker.FuncFormatter(lambda x, _: format_seconds(x)))

    axes[0].set_ylabel("Makespan Real (s)")
    axes[-1].legend(loc="upper left", fontsize=8)
    fig.suptitle(
        f"Makespan Planejado vs. Real — {project_name}",
        fontsize=14, fontweight="bold", y=1.02)
    fig.tight_layout()
    fig.savefig(os.path.join(charts_dir, "planned_vs_actual.png"),
                bbox_inches="tight")
    plt.close(fig)
    print("  [OK] planned_vs_actual.png")


# ---------- Gráfico 5: Desvio padrão da carga ------------------------------

def plot_load_stddev(agg, charts_dir, project_name):
    """Desvio padrão mediano da carga real entre workers."""
    workers_list = sorted(agg["workers"].unique())
    n_algos = len(ALGO_ORDER)
    x = range(len(workers_list))
    width = 0.8 / n_algos

    fig, ax = plt.subplots(figsize=(8, 5))

    # Use exec_makespan_stddev_ns as a proxy for load variance
    # from aggregate. But actually the raw has exec_load_stddev_s
    # Let's compute the median of exec_load_stddev_s from raw data.
    raw_path = os.path.join(os.path.dirname(charts_dir), "raw.csv")
    if os.path.exists(raw_path):
        raw = pd.read_csv(raw_path)
        for i, algo in enumerate(ALGO_ORDER):
            medians = []
            for w in workers_list:
                subset = raw[(raw["algorithm"] == algo) & (raw["workers"] == w)]
                med = subset["exec_load_stddev_s"].median()
                medians.append(med if pd.notna(med) else 0)

            positions = [xi + i * width for xi in x]
            ax.bar(positions, medians, width, label=algo,
                   color=ALGO_COLORS[algo], alpha=0.85, edgecolor="white")

    ax.set_xlabel("Número de Workers (p)")
    ax.set_ylabel("Desvio Padrão da Carga (s)")
    ax.set_title(
        f"Balanceamento de Carga entre Workers — {project_name}",
        fontweight="bold")
    ax.set_xticks([xi + width * (n_algos - 1) / 2 for xi in x])
    ax.set_xticklabels([f"p={w}" for w in workers_list])
    ax.legend(loc="upper right")

    fig.tight_layout()
    fig.savefig(os.path.join(charts_dir, "load_stddev.png"),
                bbox_inches="tight")
    plt.close(fig)
    print("  [OK] load_stddev.png")


# ---------- Gráfico 6: Painel resumo (2×2) ---------------------------------

def plot_summary_panel(raw, agg, native, t1_measured, t1_theoretical,
                       charts_dir, project_name):
    """Painel 2×2 com os 4 gráficos mais relevantes para a monografia."""
    workers_list = sorted(agg["workers"].unique())

    fig, axes = plt.subplots(2, 2, figsize=(14, 10))

    # (0,0) — Makespan bars
    ax = axes[0, 0]
    n_algos = len(ALGO_ORDER)
    x = range(len(workers_list))
    width = 0.8 / n_algos
    for i, algo in enumerate(ALGO_ORDER):
        subset = agg[agg["algorithm"] == algo].sort_values("workers")
        vals = ns_to_seconds(subset["exec_makespan_median_ns"].values)
        positions = [xi + i * width for xi in x]
        ax.bar(positions, vals, width, label=algo,
               color=ALGO_COLORS[algo], alpha=0.85, edgecolor="white")
    if native is not None and not native.empty:
        native_sorted = native.sort_values("workers")
        ax.plot([xi + width * (n_algos - 1) / 2 for xi in x],
                ns_to_seconds(native_sorted["duration_ns"]), "kD--",
                label="Go Native", linewidth=1.5, markersize=5)
    ax.set_xlabel("Workers (p)")
    ax.set_ylabel("Makespan (s)")
    ax.set_title("(a) Makespan Mediano")
    ax.set_xticks([xi + width * (n_algos - 1) / 2 for xi in x])
    ax.set_xticklabels([f"p={w}" for w in workers_list])
    ax.legend(fontsize=8)
    ax.yaxis.set_major_formatter(
        ticker.FuncFormatter(lambda x, _: format_seconds(x)))

    # (0,1) — Speedup (T1 medido)
    ax = axes[0, 1]
    t1 = t1_measured
    if t1:
        for algo in ALGO_ORDER:
            subset = agg[agg["algorithm"] == algo].sort_values("workers")
            makespans = ns_to_seconds(subset["exec_makespan_median_ns"].values)
            speedups = [t1 / m for m in makespans]
            ax.plot(workers_list, speedups, "o-", label=algo,
                    color=ALGO_COLORS[algo], linewidth=2, markersize=5)
        ax.plot(workers_list, workers_list, "k--", alpha=0.4, label="Ideal")
        if native is not None and not native.empty:
            native_sorted = native.sort_values("workers")
            ax.plot(native_sorted["workers"], native_sorted["speedup"], "kD-.",
                    label="Go Native", linewidth=1.5, markersize=5)
    ax.set_xlabel("Workers (p)")
    ax.set_ylabel("Speedup S(p)")
    ax.set_title("(b) Speedup Empírico (T1 medido)")
    ax.set_xticks(workers_list)
    ax.legend(fontsize=8)

    # (1,0) — Boxplot p=4 (mais representativo)
    ax = axes[1, 0]
    target_w = 4 if 4 in workers_list else workers_list[len(workers_list) // 2]
    data_by_algo = []
    labels = []
    colors = []
    for algo in ALGO_ORDER:
        subset = raw[(raw["algorithm"] == algo) & (raw["workers"] == target_w)]
        vals = ns_to_seconds(subset["exec_makespan_ns"].dropna())
        if not vals.empty:
            data_by_algo.append(vals.values)
            labels.append(ALGO_SHORT[algo])
            colors.append(ALGO_COLORS[algo])
    if data_by_algo:
        bp = ax.boxplot(data_by_algo, tick_labels=labels, patch_artist=True,
                        widths=0.6, medianprops=dict(color="black", linewidth=2))
        for patch, color in zip(bp["boxes"], colors):
            patch.set_facecolor(color)
            patch.set_alpha(0.7)
    ax.set_xlabel("Algoritmo")
    ax.set_ylabel("Makespan (s)")
    ax.set_title(f"(c) Distribuição p={target_w}")
    ax.yaxis.set_major_formatter(
        ticker.FuncFormatter(lambda x, _: format_seconds(x)))

    # (1,1) — Load StdDev
    ax = axes[1, 1]
    for i, algo in enumerate(ALGO_ORDER):
        medians = []
        for w in workers_list:
            subset = raw[(raw["algorithm"] == algo) & (raw["workers"] == w)]
            med = subset["exec_load_stddev_s"].median()
            medians.append(med if pd.notna(med) else 0)
        positions = [xi + i * width for xi in x]
        ax.bar(positions, medians, width, label=algo,
               color=ALGO_COLORS[algo], alpha=0.85, edgecolor="white")
    ax.set_xlabel("Workers (p)")
    ax.set_ylabel("StdDev Carga (s)")
    ax.set_title("(d) Balanceamento de Carga")
    ax.set_xticks([xi + width * (n_algos - 1) / 2 for xi in x])
    ax.set_xticklabels([f"p={w}" for w in workers_list])
    ax.legend(fontsize=8)

    fig.suptitle(f"Resumo Experimental — {project_name}",
                 fontsize=16, fontweight="bold")
    fig.tight_layout(rect=[0, 0, 1, 0.96])
    fig.savefig(os.path.join(charts_dir, "resumo.png"),
                bbox_inches="tight")
    plt.close(fig)
    print("  [OK] resumo.png")


# ---------- Main ------------------------------------------------------------

def main():
    if len(sys.argv) < 2:
        print("Uso: python scripts/generate_charts.py <run_dir>")
        print("Exemplo: python scripts/generate_charts.py "
              "benchmarks/results/cli/20260609-113627")
        sys.exit(1)

    run_dir = sys.argv[1]

    # Validar que os arquivos existem.
    for name in ("raw.csv", "aggregate.csv", "native_baselines.csv", "config.json"):
        path = os.path.join(run_dir, name)
        if not os.path.exists(path):
            print(f"Erro: {path} não encontrado.")
            sys.exit(1)

    # Carregar dados.
    raw = pd.read_csv(os.path.join(run_dir, "raw.csv"))
    agg = pd.read_csv(os.path.join(run_dir, "aggregate.csv"))
    native = pd.read_csv(os.path.join(run_dir, "native_baselines.csv"))
    with open(os.path.join(run_dir, "config.json")) as f:
        config = json.load(f)

    project_name = get_project_name(config)
    t1_measured = load_t1_measured(config)
    t1_theoretical = load_t1_theoretical(config)

    print(f"Projeto: {project_name}")
    print(f"T1 medido (baseline-seq):  "
          f"{format_seconds(t1_measured) if t1_measured else 'N/A'}")
    print(f"T1 teorico (sum durations): "
          f"{format_seconds(t1_theoretical) if t1_theoretical else 'N/A'}")
    print(f"Repeticoes: {raw.groupby(['algorithm', 'workers']).size().iloc[0]}")
    print(f"Workers: {sorted(raw['workers'].unique())}")
    print()

    # Criar diretório de gráficos.
    charts_dir = os.path.join(run_dir, "charts")
    os.makedirs(charts_dir, exist_ok=True)

    print(f"Gerando graficos em {charts_dir}/")

    plot_makespan_boxplot(raw, charts_dir, project_name)
    plot_makespan_bars(agg, native, charts_dir, project_name)
    plot_speedup(raw, agg, native, t1_measured, charts_dir, project_name)
    plot_planned_vs_actual(raw, charts_dir, project_name)
    plot_load_stddev(agg, charts_dir, project_name)
    plot_summary_panel(raw, agg, native, t1_measured, t1_theoretical,
                       charts_dir, project_name)

    print(f"\nConcluído! {6} gráficos salvos em {charts_dir}/")


if __name__ == "__main__":
    main()
