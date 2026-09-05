import { Show, createEffect, createMemo, createSignal, onCleanup, onMount } from "solid-js";
import { bitfields } from "../store/session";
import type { TorrentDetail } from "../lib/types";
import { bucketize, filePieceRanges } from "../lib/pieces";
import { pieceMapColors } from "../lib/theme";
import { themeVersion } from "../store/ui";

// Piece-map colors resolve from semantic tokens (THM-9.1): done/working
// from --progress-* (never the accent), missing from the trough token,
// hovered file from the label highlight token. Re-read on every theme
// application; the handoff literals in lib/theme TOKEN_FALLBACKS cover
// non-DOM environments.
const GAP = 1; // device px between cells

type HoverInfo = { piece: number; bucketIndex: number; file: number } | null;

export function PiecesTab(props: { hash: string; detail: TorrentDetail }) {
  let canvas: HTMLCanvasElement | undefined;
  const [hover, setHover] = createSignal<HoverInfo>(null);
  const hex = createMemo(() => bitfields()[props.hash] ?? "");
  const chunkCount = () => Math.max(0, props.detail.transfer.chunkCount);
  const chunkSize = () => Math.max(1, props.detail.transfer.chunkSize);

  // File byte offsets (cumulative) for hover highlighting.
  const fileStarts = createMemo(() => {
    const files = props.detail.files;
    const starts: number[] = [];
    let acc = 0;
    for (const file of files) {
      starts.push(acc);
      acc += file.sizeBytes;
    }
    return { starts, total: acc };
  });
  const fileRanges = createMemo(() => {
    const { starts, total } = fileStarts();
    return filePieceRanges(starts, total, chunkSize(), chunkCount());
  });

  // Cache bucketed completion fractions so hover-only redraws don't rescan
  // every piece (a 100k-piece torrent must stay cheap on each update).
  let fractionsCache: { key: string; columns: number; rows: number; cells: number[] } | null = null;
  const fractionsFor = (
    hexValue: string,
    pieces: number,
    columns: number,
    rows: number,
  ): number[] => {
    // Key on the bitfield itself: its length is ceil(pieces/8)*2, constant
    // for a torrent, so keying on length made every redraw a cache hit and
    // froze the map at its first paint (and shared cells between torrents
    // with equal piece counts).
    const key = `${hexValue}:${pieces}:${columns * rows}`;
    if (fractionsCache && fractionsCache.key === key) return fractionsCache.cells;
    const buckets = bucketize(hexValue, pieces, columns * rows);
    // Buckets are emitted oldest→newest; place each at its sequential cell.
    // Any trailing cells stay 0 (missing) over the base trough fill.
    const cells = new Array<number>(columns * rows).fill(0);
    for (let i = 0; i < buckets.length; i++) cells[i] = buckets[i].done;
    fractionsCache = { key, columns, rows, cells };
    return cells;
  };

  const draw = () => {
    const ctx = canvas?.getContext("2d");
    const el = canvas;
    if (!ctx || !el) return;
    // Tracked so an accent/theme application repaints with fresh colors.
    const palette = pieceMapColors();
    const cssWidth = el.clientWidth;
    const cssHeight = el.clientHeight;
    const dpr = window.devicePixelRatio || 1;
    const width = Math.max(1, Math.round(cssWidth * dpr));
    const height = Math.max(1, Math.round(cssHeight * dpr));
    if (el.width !== width) el.width = width;
    if (el.height !== height) el.height = height;
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    ctx.clearRect(0, 0, cssWidth, cssHeight);

    const pieces = chunkCount();
    if (!pieces || !hex()) {
      ctx.fillStyle = palette.missing;
      ctx.fillRect(0, 0, cssWidth, cssHeight);
      return;
    }

    // Bucket to the panel: ~3px cells so even 100k pieces draw in a few ms.
    const columns = Math.max(1, Math.floor(cssWidth / 3));
    const rows = Math.max(1, Math.floor(cssHeight / 3));
    const cells = fractionsFor(hex(), pieces, columns, rows);
    const hov = hover();
    const colW = cssWidth / columns;
    const rowH = cssHeight / rows;
    const perCell = Math.max(1, Math.ceil(pieces / (columns * rows)));
    const highlight = hov ? fileRanges().find((r) => r.index === hov.file) : undefined;
    // Piece range -> inclusive cell range for the highlighted file.
    const hiStart = highlight ? Math.floor(highlight.start / perCell) : 0;
    const hiEnd = highlight
      ? Math.floor((Math.max(highlight.start + 1, highlight.end) - 1) / perCell)
      : -1;

    ctx.fillStyle = palette.missing;
    ctx.fillRect(0, 0, cssWidth, cssHeight); // base trough color under any gap
    const inset = GAP / dpr;
    const drawCell = (x: number, y: number, w: number, h: number) => {
      ctx.fillRect(x + inset / 2, y + inset / 2, Math.max(1, w - inset), Math.max(1, h - inset));
    };
    for (let i = 0; i < cells.length; i++) {
      const frac = cells[i];
      const x = (i % columns) * colW;
      const y = Math.floor(i / columns) * rowH;
      let color: string;
      if (frac >= 1) color = palette.done;
      else if (frac > 0) color = palette.working;
      else color = palette.missing;
      if (highlight && i >= hiStart && i <= hiEnd) color = palette.highlight;
      ctx.fillStyle = color;
      drawCell(x, y, colW, rowH);
    }
  };

  // Redraw when the bitfield, hover state, or geometry inputs change. Solid
  // can only track reactive reads made synchronously inside the effect, so we
  // touch the signals here and defer the actual paint to the next frame.
  createEffect(() => {
    void hex();
    void hover();
    void chunkCount();
    void themeVersion();
    fileStarts();
    requestAnimationFrame(draw);
  });
  onMount(() => {
    draw();
    const observer = new ResizeObserver(() => draw());
    if (canvas) observer.observe(canvas);
    onCleanup(() => observer.disconnect());
  });

  const hoverAt = (event: MouseEvent) => {
    const rect = canvas?.getBoundingClientRect();
    const el = canvas;
    if (!rect || !el || !chunkCount() || !hex()) {
      setHover(null);
      return;
    }
    const columns = Math.max(1, Math.floor(rect.width / 3));
    const rows = Math.max(1, Math.floor(rect.height / 3));
    const col = Math.min(
      columns - 1,
      Math.max(0, Math.floor((event.clientX - rect.left) / (rect.width / columns))),
    );
    const row = Math.min(
      rows - 1,
      Math.max(0, Math.floor((event.clientY - rect.top) / (rect.height / rows))),
    );
    const cell = row * columns + col;
    // Cells map uniformly onto pieces (see draw/perCell); hovered cell is the
    // piece at its midpoint. Cells beyond pieceCount are empty (missing).
    const perCell = Math.max(1, Math.ceil(chunkCount() / (columns * rows)));
    const piece = cell * perCell + Math.floor(perCell / 2);
    if (piece >= chunkCount()) {
      setHover(null);
      return;
    }
    const ranges = fileRanges();
    let file = -1;
    for (let i = 0; i < ranges.length; i++) {
      if (piece >= ranges[i].start && piece < ranges[i].end) {
        file = i;
        break;
      }
    }
    setHover({ piece, bucketIndex: cell, file });
  };

  return (
    <div class="pieces-tab">
      <Show
        when={chunkCount() > 0 && hex()}
        fallback={
          <div class="detail-empty-rows">
            No piece data yet — it arrives with the first detail tick.{" "}
            <button type="button" class="copy-button" onClick={() => draw()}>
              Retry
            </button>
          </div>
        }
      >
        <div class="piece-canvas-wrap">
          <canvas
            ref={canvas}
            class="piece-canvas"
            role="img"
            aria-label={`${props.detail.transfer.chunksDone} of ${chunkCount()} pieces complete`}
            onMouseMove={hoverAt}
            onMouseLeave={() => setHover(null)}
          />
        </div>
      </Show>
      <Show
        when={hover()}
        fallback={
          <span class="tnum">
            {props.detail.transfer.chunksDone.toLocaleString()} / {chunkCount().toLocaleString()}{" "}
            pieces complete
          </span>
        }
      >
        {(h) => (
          <span class="piece-hover tnum">
            Piece {h().piece.toLocaleString()} ·{" "}
            {h().file >= 0 ? (props.detail.files[h().file]?.path ?? "—") : "—"}
          </span>
        )}
      </Show>
    </div>
  );
}
