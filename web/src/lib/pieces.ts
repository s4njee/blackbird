/** Piece-map helpers for the Pieces tab (PAR-2.6). Pure logic so the decode
 * and bucketing are unit-testable without a DOM. */

export type PieceState = "done" | "missing";

/** Reads the bit for a piece index from a hex bitfield (MSB = piece 0). */
export function isPieceDone(hex: string, piece: number): boolean {
  const byteIndex = piece >> 3;
  const hexIndex = byteIndex * 2;
  if (hexIndex + 1 >= hex.length) return false;
  const byte = parseInt(hex.slice(hexIndex, hexIndex + 2), 16);
  if (!Number.isFinite(byte)) return false;
  const bit = 7 - (piece & 7); // MSB first within the byte
  return ((byte >> bit) & 1) === 1;
}

/** How many hex characters cover `pieceCount` pieces (ceil(pieces/8)*2). */
export function bitfieldHexLength(pieceCount: number): number {
  return Math.ceil(Math.max(0, pieceCount) / 8) * 2;
}

export type Bucket = {
  /** First piece index covered by this bucket (inclusive). */
  start: number;
  /** One past the last piece index covered (exclusive). */
  end: number;
  /** Fraction of pieces in [start,end) that are done (0..1). */
  done: number;
};

/**
 * Buckets `pieceCount` pieces into `columns` buckets of (near-)equal width,
 * computing each bucket's completion fraction from the hex bitfield. A bucket
 * whose pieces are all done reports 1; a fully missing bucket reports 0 so
 * callers can color done / partial / missing distinctly.
 */
export function bucketize(hex: string, pieceCount: number, columns: number): Bucket[] {
  const count = Math.max(0, Math.floor(pieceCount));
  const cols = Math.max(1, Math.floor(columns));
  if (count === 0) return [];
  const buckets: Bucket[] = [];
  const perBucket = Math.max(1, Math.ceil(count / cols));
  for (let start = 0; start < count; start += perBucket) {
    const end = Math.min(count, start + perBucket);
    let done = 0;
    for (let piece = start; piece < end; piece++) {
      if (isPieceDone(hex, piece)) done++;
    }
    buckets.push({ start, end, done: done / (end - start) });
  }
  return buckets;
}

export type FilePieceRange = { index: number; start: number; end: number };

/**
 * Maps files to piece ranges. `byteStarts[i]` is the byte offset of file i's
 * first byte (computed by cumulating each file's sizeBytes); `totalBytes` is
 * the torrent size. Returns each file's [start, end) piece span.
 */
export function filePieceRanges(
  byteStarts: number[],
  totalBytes: number,
  chunkSize: number,
  pieceCount: number,
): FilePieceRange[] {
  const size = Math.max(1, chunkSize);
  const count = Math.max(0, Math.floor(pieceCount));
  return byteStarts.map((startByte, index) => {
    const start = Math.max(0, Math.min(count, Math.floor(startByte / size)));
    const endByte = index + 1 < byteStarts.length ? byteStarts[index + 1] : totalBytes;
    const end = Math.max(start, Math.min(count, Math.ceil(endByte / size)));
    return { index, start, end };
  });
}
