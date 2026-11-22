package app

import (
	"encoding/binary"
	"os"
	"strings"
)

// readAudioTags 读歌名、歌手、专辑。只认最常见的两种容器：FLAC 的
// Vorbis 注释与 MP3 的 ID3v2。读不出来就返回空串，交给文件名去猜。
func readAudioTags(path string) (title, artist, album string) {
	f, err := os.Open(path)
	if err != nil {
		return "", "", ""
	}
	defer f.Close()
	var head [10]byte
	if _, err := f.ReadAt(head[:], 0); err != nil {
		return "", "", ""
	}
	switch {
	case string(head[:4]) == "fLaC":
		t, a, al := flacTags(f)
		return t, a, al
	case string(head[:3]) == "ID3":
		t, a, al := id3Tags(f, head[:])
		return t, a, al
	}
	return "", "", ""
}

// flacTags 走 FLAC 的元数据块，取类型 4（VORBIS_COMMENT）里的键值。
func flacTags(f *os.File) (title, artist, album string) {
	at := int64(4)
	for i := 0; i < 64; i++ {
		var hdr [4]byte
		if _, err := f.ReadAt(hdr[:], at); err != nil {
			return
		}
		last := hdr[0]&0x80 != 0
		kind := hdr[0] & 0x7F
		size := int64(hdr[1])<<16 | int64(hdr[2])<<8 | int64(hdr[3])
		body := make([]byte, size)
		if _, err := f.ReadAt(body, at+4); err != nil {
			return
		}
		if kind == 4 {
			// 厂商字符串 + 条目数，之后是 key=value
			if len(body) < 4 {
				return
			}
			vendor := binary.LittleEndian.Uint32(body[0:4])
			pos := 4 + int(vendor)
			if pos+4 > len(body) {
				return
			}
			count := binary.LittleEndian.Uint32(body[pos : pos+4])
			pos += 4
			for n := uint32(0); n < count && pos+4 <= len(body); n++ {
				ln := int(binary.LittleEndian.Uint32(body[pos : pos+4]))
				pos += 4
				if pos+ln > len(body) {
					return
				}
				entry := string(body[pos : pos+ln])
				pos += ln
				key, value, ok := strings.Cut(entry, "=")
				if !ok {
					continue
				}
				switch strings.ToLower(key) {
				case "title":
					if title == "" {
						title = value
					}
				case "artist":
					if artist == "" {
						artist = value
					}
				case "album":
					if album == "" {
						album = value
					}
				}
			}
			return
		}
		at += 4 + size
		if last {
			return
		}
	}
	return
}

// id3Tags 读 ID3v2 的 TIT2 / TPE1 / TALB。帧大小是 synchsafe 的。
func id3Tags(f *os.File, head []byte) (title, artist, album string) {
	size := int64(head[6]&0x7F)<<21 | int64(head[7]&0x7F)<<14 |
		int64(head[8]&0x7F)<<7 | int64(head[9]&0x7F)
	body := make([]byte, size)
	if _, err := f.ReadAt(body, 10); err != nil {
		return
	}
	pos := 0
	// 扩展头：前 4 字节是它自己的长度
	if head[5]&0x40 != 0 && len(body) > 4 {
		ext := int64(body[0])<<24 | int64(body[1])<<16 | int64(body[2])<<8 | int64(body[3])
		pos = int(ext)
	}
	for pos+10 <= len(body) {
		id := string(body[pos : pos+4])
		if id == "\x00\x00\x00\x00" {
			break
		}
		fsize := int64(body[pos+4])<<21 | int64(body[pos+5])<<14 |
			int64(body[pos+6])<<7 | int64(body[pos+7])
		pos += 10
		if fsize <= 0 || pos+int(fsize) > len(body) {
			break
		}
		frame := body[pos : pos+int(fsize)]
		pos += int(fsize)
		text := id3Text(frame)
		switch id {
		case "TIT2":
			title = text
		case "TPE1":
			artist = text
		case "TALB":
			album = text
		}
	}
	return
}

// id3Text 去掉帧开头的编码字节，按编码解码。
func id3Text(frame []byte) string {
	if len(frame) < 2 {
		return ""
	}
	enc := frame[0]
	data := frame[1:]
	switch enc {
	case 0, 3: // Latin-1 / UTF-8
		return strings.TrimRight(string(data), "\x00")
	case 1, 2: // UTF-16 带 BOM / 无 BOM
		return utf16String(data, enc == 1)
	}
	return ""
}

// utf16String 把 UTF-16 字节转成 UTF-8。
func utf16String(data []byte, bom bool) string {
	bigEndian := false
	if bom && len(data) >= 2 {
		if data[0] == 0xFE && data[1] == 0xFF {
			bigEndian = true
		}
		data = data[2:]
	}
	var sb strings.Builder
	for i := 0; i+1 < len(data); i += 2 {
		var v uint16
		if bigEndian {
			v = uint16(data[i])<<8 | uint16(data[i+1])
		} else {
			v = uint16(data[i+1])<<8 | uint16(data[i])
		}
		if v == 0 {
			break
		}
		sb.WriteRune(rune(v))
	}
	return sb.String()
}

// readEmbeddedCover 从音频标签里取封面（FLAC 的 PICTURE 块 / MP3 的 APIC 帧）。
// 优先正面图（type 3），没有就用第一张。
func readEmbeddedCover(path string) []byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var head [10]byte
	if _, err := f.ReadAt(head[:], 0); err != nil {
		return nil
	}
	switch {
	case string(head[:4]) == "fLaC":
		return flacCover(f)
	case string(head[:3]) == "ID3":
		return id3Cover(f, head[:])
	}
	return nil
}

func flacCover(f *os.File) []byte {
	at := int64(4)
	var first []byte
	for i := 0; i < 64; i++ {
		var hdr [4]byte
		if _, err := f.ReadAt(hdr[:], at); err != nil {
			return first
		}
		last := hdr[0]&0x80 != 0
		kind := hdr[0] & 0x7F
		size := int64(hdr[1])<<16 | int64(hdr[2])<<8 | int64(hdr[3])
		if kind == 6 && size > 32 && size < 32<<20 {
			body := make([]byte, size)
			if _, err := f.ReadAt(body, at+4); err != nil {
				return first
			}
			if data, picType := parseFlacPicture(body); data != nil {
				if picType == 3 {
					return data
				}
				if first == nil {
					first = data
				}
			}
		}
		at += 4 + size
		if last {
			return first
		}
	}
	return first
}

// parseFlacPicture 读 FLAC 的 PICTURE 块：类型、MIME、描述、宽高深色数，最后是图片数据。
func parseFlacPicture(body []byte) ([]byte, int) {
	if len(body) < 8 {
		return nil, 0
	}
	picType := int(binary.BigEndian.Uint32(body[0:4]))
	mimeLen := int(binary.BigEndian.Uint32(body[4:8]))
	pos := 8 + mimeLen
	if pos+4 > len(body) {
		return nil, 0
	}
	descLen := int(binary.BigEndian.Uint32(body[pos : pos+4]))
	pos += 4 + descLen
	if pos+20 > len(body) {
		return nil, 0
	}
	pos += 16 // 宽、高、色深、索引色数
	dataLen := int(binary.BigEndian.Uint32(body[pos : pos+4]))
	pos += 4
	if dataLen <= 0 || pos+dataLen > len(body) {
		return nil, 0
	}
	return body[pos : pos+dataLen], picType
}

func id3Cover(f *os.File, head []byte) []byte {
	size := int64(head[6]&0x7F)<<21 | int64(head[7]&0x7F)<<14 |
		int64(head[8]&0x7F)<<7 | int64(head[9]&0x7F)
	body := make([]byte, size)
	if _, err := f.ReadAt(body, 10); err != nil {
		return nil
	}
	pos := 0
	if head[5]&0x40 != 0 && len(body) > 4 {
		ext := int64(body[0])<<24 | int64(body[1])<<16 | int64(body[2])<<8 | int64(body[3])
		pos = int(ext)
	}
	for pos+10 <= len(body) {
		id := string(body[pos : pos+4])
		if id == "\x00\x00\x00\x00" {
			return nil
		}
		fsize := int64(body[pos+4])<<21 | int64(body[pos+5])<<14 |
			int64(body[pos+6])<<7 | int64(body[pos+7])
		pos += 10
		if fsize <= 0 || pos+int(fsize) > len(body) {
			return nil
		}
		frame := body[pos : pos+int(fsize)]
		pos += int(fsize)
		if id == "APIC" {
			if data := parseAPIC(frame); data != nil {
				return data
			}
		}
	}
	return nil
}

// parseAPIC 读 APIC 帧：编码、MIME（Latin-1 结尾 0）、图片类型、描述（按编码结尾 0）、数据。
func parseAPIC(frame []byte) []byte {
	if len(frame) < 4 {
		return nil
	}
	enc := frame[0]
	rest := frame[1:]
	end := 0
	for end < len(rest) && rest[end] != 0 {
		end++
	}
	mime := string(rest[:end])
	if end+1 >= len(rest) {
		return nil
	}
	rest = rest[end+1:]
	if len(rest) < 1 {
		return nil
	}
	rest = rest[1:] // 图片类型
	// 描述：UTF-16 时两字节一字符，结尾是两个 0
	if enc == 1 || enc == 2 {
		i := 0
		for i+1 < len(rest) {
			if rest[i] == 0 && rest[i+1] == 0 {
				i += 2
				break
			}
			i += 2
		}
		rest = rest[min(i, len(rest)):]
	} else {
		i := 0
		for i < len(rest) && rest[i] != 0 {
			i++
		}
		if i < len(rest) {
			i++
		}
		rest = rest[min(i, len(rest)):]
	}
	if len(rest) == 0 {
		return nil
	}
	_ = mime
	return rest
}
