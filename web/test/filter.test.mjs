import assert from "node:assert/strict";
import { TorrentSearchIndex, parseQuery } from "../.test-dist/filter.js";
import { IncrementalTorrentSorter } from "../.test-dist/sort.js";

const torrent = {
  hash: "ABCDEF012345", name: "Ubuntu Server", basePath: "/downloads/linux", directory: "/downloads/linux",
  trackerHost: "tracker.example", message: "", label: "iso", state: "downloading", complete: false, isOpen: true,
  downRate: 100, upRate: 0, ratio: 1.5, sizeBytes: 3 * 1024 ** 3,
};
const index = new TorrentSearchIndex();
index.update(torrent);
assert.equal(index.matches(torrent, parseQuery("ubuntu abc path:linux tracker:example label:iso status:active ratio>1 size<4gb")), true);
assert.equal(index.matches(torrent, parseQuery("status:inactive")), false);
assert.equal(index.matches(torrent, parseQuery("size<1gb")), false);
index.update({ ...torrent, name: "Debian Server", label: "archive", downRate: 0 });
const changed = { ...torrent, name: "Debian Server", label: "archive", downRate: 0 };
assert.equal(index.matches(changed, parseQuery("debian label:archive status:inactive")), true);
assert.equal(index.matches(changed, parseQuery("ubuntu")), false);
index.remove(changed.hash);
assert.equal(index.matches(changed, parseQuery("debian")), false);

const sorter = new IncrementalTorrentSorter();
const beta = { ...torrent, hash: "B", name: "Beta", downRate: 0 };
const alpha = { ...torrent, hash: "A", name: "Alpha", downRate: 0 };
assert.deepEqual(sorter.sort([beta, alpha], [{ column: "name", direction: "asc" }]).map((item) => item.hash), ["A", "B"]);
const moved = { ...beta, name: "0 Beta" };
assert.deepEqual(sorter.sort([moved, alpha], [{ column: "name", direction: "asc" }]).map((item) => item.hash), ["B", "A"]);
assert.deepEqual(sorter.sort([moved, alpha], [{ column: "name", direction: "asc" }, { column: "hash", direction: "desc" }]).map((item) => item.hash), ["B", "A"]);
