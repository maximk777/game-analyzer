#!/usr/bin/env python3
"""Extract the card references the recogniser needs from the installed client.

CoinPoker is an Electron app, so its interface assets sit in a plain `.asar`
archive. Two of them decide what a card is:

  * the four suit sprites, which are the pips drawn on every card, and
  * Fira Sans, the typeface the ranks are set in.

Rendering the reference glyphs from the client's own font and comparing shapes
answers "which card is this" directly, instead of asking a text recogniser to
read a picture of a known shape as though it were unknown writing.

Nothing here is redistributed: the assets are read from the copy already
installed on this machine and written under bin/, which is not in the
repository. Without them the recogniser falls back to text recognition, so this
step is optional.
"""

import json
import math
import os
import re
import struct
import sys
import zlib

DEFAULT_ASAR = "/Applications/CoinPoker.app/Contents/Resources/app.asar"
DEFAULT_OUT = "bin/assets/coinpoker"

# The suit sprites, and the weights of Fira Sans the ranks might be set in.
WANTED = re.compile(
    r"/(spade|heart|diamond|clubs)\.[0-9a-f]+\.webp$"
    r"|Fira-Sans-normal-(400|700|900)\.[0-9a-f]+\.woff$"
)


def read_archive(path):
    """Returns (file handle, {path: metadata}, offset of the file body)."""
    f = open(path, "rb")
    # asar's header is a pickle: four uint32s, then the JSON directory.
    _, header_pickle_size, _, json_len = struct.unpack("<4I", f.read(16))
    header = json.loads(f.read(json_len).decode("utf-8"))
    body_offset = 8 + header_pickle_size

    files = {}

    def walk(node, prefix=""):
        for name, meta in node.get("files", {}).items():
            full = prefix + "/" + name
            if "files" in meta:
                walk(meta, full)
            else:
                files[full] = meta

    walk(header)
    return f, files, body_offset


def woff_to_ttf(data):
    """Unpacks a WOFF 1.0 container back into the TrueType file inside it.

    CoreText cannot load WOFF, and the client ships nothing else, so the font
    has to be reassembled: a WOFF is the original sfnt tables, each separately
    zlib-compressed, behind a different directory.
    """
    sig, flavor, _, num_tables = struct.unpack(">4sIIH", data[:14])
    if sig != b"wOFF":
        raise ValueError("not a WOFF file")

    entries = []
    off = 44
    for _ in range(num_tables):
        tag, toff, comp_len, orig_len, checksum = struct.unpack(">4sIIII", data[off:off + 20])
        entries.append((tag, toff, comp_len, orig_len, checksum))
        off += 20

    search_range = (2 ** int(math.log2(num_tables))) * 16
    entry_selector = int(math.log2(num_tables))
    range_shift = num_tables * 16 - search_range

    out = bytearray(struct.pack(">IHHHH", flavor, num_tables, search_range,
                                entry_selector, range_shift))
    body = bytearray()
    data_start = 12 + num_tables * 16
    directory = []

    for tag, toff, comp_len, orig_len, checksum in sorted(entries, key=lambda e: e[0]):
        raw = data[toff:toff + comp_len]
        table = zlib.decompress(raw) if comp_len != orig_len else raw
        if len(table) != orig_len:
            raise ValueError(f"table {tag!r} decompressed to the wrong size")
        directory.append((tag, checksum, data_start + len(body), orig_len))
        body += table
        while len(body) % 4:
            body += b"\0"

    for tag, checksum, pos, orig_len in directory:
        out += struct.pack(">4sIII", tag, checksum, pos, orig_len)
    return bytes(out) + bytes(body)


def main():
    asar = sys.argv[1] if len(sys.argv) > 1 else DEFAULT_ASAR
    out_dir = sys.argv[2] if len(sys.argv) > 2 else DEFAULT_OUT

    if not os.path.exists(asar):
        print(f"client archive not found at {asar}; card templates will be unavailable")
        return 0

    os.makedirs(out_dir, exist_ok=True)
    f, files, body_offset = read_archive(asar)

    sprites = 0
    fonts = 0
    for path, meta in files.items():
        if not WANTED.search(path):
            continue
        f.seek(body_offset + int(meta["offset"]))
        data = f.read(meta["size"])
        name = path.split("/")[-1]

        if name.endswith(".woff"):
            weight = name.split("-")[-1].split(".")[0]
            try:
                ttf = woff_to_ttf(data)
            except Exception as err:  # noqa: BLE001 - a bad font is not fatal
                print(f"  skipping {name}: {err}")
                continue
            with open(os.path.join(out_dir, f"FiraSans-{weight}.ttf"), "wb") as o:
                o.write(ttf)
            fonts += 1
        else:
            with open(os.path.join(out_dir, name), "wb") as o:
                o.write(data)
            sprites += 1

    f.close()
    print(f"card templates: {sprites} suit sprites, {fonts} font weights -> {out_dir}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
