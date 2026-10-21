"""Strictly bounded Dagor stream backends; callers validate framing."""
import lzma

MAX_BYTES = 512 * 1024**2


def decompress(data: bytes, expected: int, codec: int) -> bytes:
    if not isinstance(expected, int) or not 0 <= expected <= MAX_BYTES:
        raise ValueError(f'invalid decompressed size {expected}')
    if not data or len(data) > MAX_BYTES:
        raise ValueError('invalid compressed input length')
    if codec == 2:
        if not expected:
            raise ValueError('Oodle needs a validated declared output size')
        import ooz
        result = ooz.decompress(data, expected)
    elif codec == 1:
        import zstandard as zstd
        # Some lmap ZSTD streams omit an outer decompressed-size prefix.
        budget = expected or MAX_BYTES
        with zstd.ZstdDecompressor(max_window_size=MAX_BYTES // 1024).stream_reader(data) as stream:
            chunks = []
            total = 0
            while True:
                part = stream.read(min(8 * 1024**2, budget + 1 - total))
                if not part:
                    break
                total += len(part)
                if total > budget:
                    raise ValueError('ZSTD output exceeds output budget')
                chunks.append(part)
            result = b''.join(chunks)
    elif codec == 0:
        budget = expected or MAX_BYTES
        dec = lzma.LZMADecompressor(format=lzma.FORMAT_ALONE, memlimit=MAX_BYTES)
        # Dagor may omit the 8-byte size field from the LZMA-alone header.
        try:
            result = dec.decompress(data, max_length=budget + 1)
        except lzma.LZMAError:
            dec = lzma.LZMADecompressor(format=lzma.FORMAT_ALONE, memlimit=MAX_BYTES)
            declared = expected.to_bytes(8, 'little') if expected else b'\xff' * 8
            result = dec.decompress(data[:5] + declared + data[5:], max_length=budget + 1)
        if not dec.eof and not dec.needs_input and len(result) <= budget:
            result += dec.decompress(b'', max_length=budget + 1 - len(result))
        if len(result) > budget:
            raise ValueError('LZMA output exceeds output budget')
    else:
        raise ValueError(f'unsupported codec {codec}')
    if expected and len(result) != expected:
        raise ValueError(f'decoded {len(result)} bytes, expected {expected}')
    return bytes(result)
