package capture

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"time"
)

// Format is the file format of a capture.
type Format string

// The two formats of the spec (`CaptureRequest.format`).
const (
	PCAPNG Format = "pcapng"
	PCAP   Format = "pcap"
)

// Ext is the file name extension of the format.
func (f Format) Ext() string { return "." + string(f) }

// ContentType is the media type a download of the format has.
func (f Format) ContentType() string {
	if f == PCAP {
		return "application/vnd.tcpdump.pcap"
	}
	return "application/x-pcapng"
}

// InterfaceInfo describes an interface of a capture: pcapng keeps it in an interface description block, pcap has
// room for one link type only.
type InterfaceInfo struct {
	Name        string
	Description string
	Link        LinkType
	Snaplen     int
}

// Packet is one captured packet.
type Packet struct {
	// Iface is the index of the interface in the capture's list.
	Iface int
	Time  time.Time
	// Data are the captured bytes (cut to the snap length), OrigLen the length on the wire.
	Data    []byte
	OrigLen int
	// Outgoing is set for a packet the interface sent, else it received it (pcapng only).
	Outgoing bool
}

// The block types and option codes of pcapng (draft-ietf-opsawg-pcapng).
const (
	blockSHB = 0x0A0D0D0A
	blockIDB = 0x00000001
	blockEPB = 0x00000006

	byteOrderMagic = 0x1A2B3C4D

	optEnd         = 0
	optShbUserAppl = 4
	optIfName      = 2
	optIfDesc      = 3
	optIfTsResol   = 9
	optEpbFlags    = 2

	pcapMagicMicro   = 0xa1b2c3d4
	pcapMagicNano    = 0xa1b23c4d
	pcapHeaderLen    = 24
	pcapRecordHeader = 16
)

// Writer writes the packets of one file.
type Writer struct {
	bw     *bufio.Writer
	format Format
	ifaces []InterfaceInfo
	buf    []byte
	n      int64 // bytes written, including those that are still buffered
	frames int64
	err    error
}

// HeaderSize returns how many bytes the headers of a file of the format with these interfaces take, and with
// them the size of an empty file.
func HeaderSize(format Format, ifaces []InterfaceInfo, app string) int {
	if format == PCAP {
		return pcapHeaderLen
	}
	n := len(shb(app))
	for i := range ifaces {
		n += len(idb(&ifaces[i]))
	}
	return n
}

// PacketSize returns how many bytes the record of a packet of dataLen captured bytes takes in a file of the
// format.
func PacketSize(format Format, dataLen int, withFlags bool) int {
	if format == PCAP {
		return pcapRecordHeader + dataLen
	}
	n := 32 + pad4(dataLen)
	if withFlags {
		n += 8 + 4 // epb_flags and opt_endofopt
	}
	return n
}

func pad4(n int) int { return (n + 3) &^ 3 }

// NewWriter starts a file on w: the headers are written at once (not yet flushed). A pcap file has the one
// link type of its interfaces; interfaces of different link types need pcapng.
func NewWriter(w io.Writer, format Format, ifaces []InterfaceInfo, app string) (*Writer, error) {
	if len(ifaces) == 0 {
		return nil, errors.New("a capture file needs an interface")
	}
	wr := &Writer{bw: bufio.NewWriterSize(w, 256<<10), format: format, ifaces: ifaces}
	switch format {
	case PCAP:
		for _, i := range ifaces {
			if i.Link != ifaces[0].Link {
				return nil, errors.New("the format pcap holds one link type: use pcapng for interfaces of different link types")
			}
		}
		hdr := make([]byte, pcapHeaderLen)
		binary.LittleEndian.PutUint32(hdr[0:], pcapMagicMicro)
		binary.LittleEndian.PutUint16(hdr[4:], 2)
		binary.LittleEndian.PutUint16(hdr[6:], 4)
		snap := 0
		for _, i := range ifaces {
			snap = max(snap, i.Snaplen)
		}
		binary.LittleEndian.PutUint32(hdr[16:], uint32(snap))
		binary.LittleEndian.PutUint32(hdr[20:], uint32(ifaces[0].Link))
		wr.put(hdr)
	case PCAPNG:
		wr.put(shb(app))
		for i := range ifaces {
			wr.put(idb(&ifaces[i]))
		}
	default:
		return nil, fmt.Errorf("unknown capture format %q", format)
	}
	return wr, wr.err
}

func (w *Writer) put(b []byte) {
	if w.err != nil {
		return
	}
	n, err := w.bw.Write(b)
	w.n += int64(n)
	w.err = err
}

// WritePacket appends a packet. A pcap file keeps the time to the microsecond.
func (w *Writer) WritePacket(p Packet) error {
	if p.Iface < 0 || p.Iface >= len(w.ifaces) {
		return fmt.Errorf("the packet is from interface %d, the file has %d", p.Iface, len(w.ifaces))
	}
	if w.format == PCAP {
		b := w.scratch(pcapRecordHeader + len(p.Data))
		binary.LittleEndian.PutUint32(b[0:], uint32(p.Time.Unix()))
		binary.LittleEndian.PutUint32(b[4:], uint32(p.Time.Nanosecond()/1000))
		binary.LittleEndian.PutUint32(b[8:], uint32(len(p.Data)))
		binary.LittleEndian.PutUint32(b[12:], uint32(p.OrigLen))
		copy(b[pcapRecordHeader:], p.Data)
		w.put(b)
	} else {
		n := PacketSize(PCAPNG, len(p.Data), true)
		b := w.scratch(n)
		ns := uint64(p.Time.UnixNano())
		binary.LittleEndian.PutUint32(b[0:], blockEPB)
		binary.LittleEndian.PutUint32(b[4:], uint32(n))
		binary.LittleEndian.PutUint32(b[8:], uint32(p.Iface))
		binary.LittleEndian.PutUint32(b[12:], uint32(ns>>32))
		binary.LittleEndian.PutUint32(b[16:], uint32(ns))
		binary.LittleEndian.PutUint32(b[20:], uint32(len(p.Data)))
		binary.LittleEndian.PutUint32(b[24:], uint32(p.OrigLen))
		off := 28
		copy(b[off:], p.Data)
		off += pad4(len(p.Data))
		flags := uint32(1) // inbound
		if p.Outgoing {
			flags = 2
		}
		binary.LittleEndian.PutUint16(b[off:], optEpbFlags)
		binary.LittleEndian.PutUint16(b[off+2:], 4)
		binary.LittleEndian.PutUint32(b[off+4:], flags)
		binary.LittleEndian.PutUint32(b[off+8:], optEnd)
		binary.LittleEndian.PutUint32(b[n-4:], uint32(n))
		w.put(b)
	}
	w.frames++
	return w.err
}

// scratch returns a zeroed buffer of n bytes; the writer reuses it, put copies it into the buffered writer.
func (w *Writer) scratch(n int) []byte {
	if cap(w.buf) < n {
		w.buf = make([]byte, n, max(n, 2048))
	}
	b := w.buf[:n]
	clear(b)
	return b
}

// Flush hands the buffered bytes to the file.
func (w *Writer) Flush() error {
	if w.err == nil {
		w.err = w.bw.Flush()
	}
	return w.err
}

// Size is the number of bytes written so far, also those still in the buffer.
func (w *Writer) Size() int64 { return w.n }

// Packets is the number of packets written.
func (w *Writer) Packets() int64 { return w.frames }

func option(code uint16, val []byte) []byte {
	b := make([]byte, 4+pad4(len(val)))
	binary.LittleEndian.PutUint16(b[0:], code)
	binary.LittleEndian.PutUint16(b[2:], uint16(len(val)))
	copy(b[4:], val)
	return b
}

func block(typ uint32, body []byte) []byte {
	n := 12 + pad4(len(body))
	b := make([]byte, n)
	binary.LittleEndian.PutUint32(b[0:], typ)
	binary.LittleEndian.PutUint32(b[4:], uint32(n))
	copy(b[8:], body)
	binary.LittleEndian.PutUint32(b[n-4:], uint32(n))
	return b
}

func shb(app string) []byte {
	body := make([]byte, 16)
	binary.LittleEndian.PutUint32(body[0:], byteOrderMagic)
	binary.LittleEndian.PutUint16(body[4:], 1)
	binary.LittleEndian.PutUint16(body[6:], 0)
	binary.LittleEndian.PutUint64(body[8:], ^uint64(0)) // the length of the section is not known
	if app != "" {
		body = append(body, option(optShbUserAppl, []byte(app))...)
	}
	body = append(body, option(optEnd, nil)...)
	return block(blockSHB, body)
}

func idb(i *InterfaceInfo) []byte {
	body := make([]byte, 8)
	binary.LittleEndian.PutUint16(body[0:], uint16(i.Link))
	binary.LittleEndian.PutUint32(body[4:], uint32(i.Snaplen))
	if i.Name != "" {
		body = append(body, option(optIfName, []byte(i.Name))...)
	}
	if i.Description != "" {
		body = append(body, option(optIfDesc, []byte(i.Description))...)
	}
	body = append(body, option(optIfTsResol, []byte{9})...) // nanoseconds
	body = append(body, option(optEnd, nil)...)
	return block(blockIDB, body)
}

// ---- reading

// ErrTruncated is returned by a Reader at a record that is cut off: the file is being written or was cut.
var ErrTruncated = errors.New("capture: the file ends inside a record")

// Reader reads the packets of a capture file of either format.
type Reader struct {
	r      *bufio.Reader
	format Format
	order  binary.ByteOrder
	ifaces []InterfaceInfo
	tsDiv  []int64 // nanoseconds per timestamp unit, per interface (pcapng)
	nano   bool    // a pcap file with nanosecond time stamps
}

// NewReader reads the header of the file.
func NewReader(r io.Reader) (*Reader, error) {
	br := bufio.NewReaderSize(r, 64<<10)
	head, err := br.Peek(4)
	if err != nil {
		return nil, fmt.Errorf("capture: not a capture file: %w", err)
	}
	rd := &Reader{r: br}
	switch {
	case binary.LittleEndian.Uint32(head) == blockSHB:
		rd.format = PCAPNG
	case binary.LittleEndian.Uint32(head) == pcapMagicMicro, binary.LittleEndian.Uint32(head) == pcapMagicNano,
		binary.BigEndian.Uint32(head) == pcapMagicMicro, binary.BigEndian.Uint32(head) == pcapMagicNano:
		rd.format = PCAP
	default:
		return nil, errors.New("capture: not a pcap or pcapng file")
	}
	if rd.format == PCAP {
		hdr := make([]byte, pcapHeaderLen)
		if _, err := io.ReadFull(br, hdr); err != nil {
			return nil, ErrTruncated
		}
		magic := binary.LittleEndian.Uint32(hdr)
		rd.order = binary.LittleEndian
		if magic != pcapMagicMicro && magic != pcapMagicNano {
			rd.order = binary.BigEndian
			magic = binary.BigEndian.Uint32(hdr)
		}
		rd.nano = magic == pcapMagicNano
		rd.ifaces = []InterfaceInfo{{Link: LinkType(rd.order.Uint32(hdr[20:])), Snaplen: int(rd.order.Uint32(hdr[16:]))}}
	}
	return rd, nil
}

// Format is the format of the file.
func (r *Reader) Format() Format { return r.format }

// Interfaces are the interfaces seen so far: all of a pcap file, those of the blocks read in pcapng.
func (r *Reader) Interfaces() []InterfaceInfo { return r.ifaces }

// Next returns the next packet, io.EOF at the end of the file and ErrTruncated at a record that is cut off.
func (r *Reader) Next() (Packet, error) {
	if r.format == PCAP {
		return r.nextPcap()
	}
	return r.nextPcapng()
}

func (r *Reader) nextPcap() (Packet, error) {
	hdr := make([]byte, pcapRecordHeader)
	if n, err := io.ReadFull(r.r, hdr); err != nil {
		if n == 0 && errors.Is(err, io.EOF) {
			return Packet{}, io.EOF
		}
		return Packet{}, ErrTruncated
	}
	incl, orig := int(r.order.Uint32(hdr[8:])), int(r.order.Uint32(hdr[12:]))
	if incl > 1<<24 {
		return Packet{}, errors.New("capture: a record is longer than 16 MiB")
	}
	data := make([]byte, incl)
	if _, err := io.ReadFull(r.r, data); err != nil {
		return Packet{}, ErrTruncated
	}
	frac := int64(r.order.Uint32(hdr[4:]))
	if !r.nano {
		frac *= 1000
	}
	return Packet{Time: time.Unix(int64(r.order.Uint32(hdr[0:])), frac), Data: data, OrigLen: orig}, nil
}

func (r *Reader) nextPcapng() (Packet, error) {
	for {
		typ, body, err := r.block()
		if err != nil {
			return Packet{}, err
		}
		switch typ {
		case blockSHB:
			if len(body) < 16 {
				return Packet{}, errors.New("capture: a section header is too short")
			}
			switch binary.LittleEndian.Uint32(body) {
			case byteOrderMagic:
				r.order = binary.LittleEndian
			case 0x4D3C2B1A:
				r.order = binary.BigEndian
			default:
				return Packet{}, errors.New("capture: unknown byte order")
			}
			// a new section starts the interfaces over
			r.ifaces, r.tsDiv = nil, nil
		case blockIDB:
			if len(body) < 8 {
				return Packet{}, errors.New("capture: an interface description is too short")
			}
			info := InterfaceInfo{Link: LinkType(r.order.Uint16(body)), Snaplen: int(r.order.Uint32(body[4:]))}
			div := int64(1000) // the default resolution is a microsecond
			r.options(body[8:], func(code uint16, val []byte) {
				switch {
				case code == optIfName:
					info.Name = string(val)
				case code == optIfDesc:
					info.Description = string(val)
				case code == optIfTsResol && len(val) == 1 && val[0]&0x80 == 0 && val[0] <= 9:
					div = 1
					for i := val[0]; i < 9; i++ {
						div *= 10
					}
				}
			})
			r.ifaces = append(r.ifaces, info)
			r.tsDiv = append(r.tsDiv, div)
		case blockEPB:
			if len(body) < 20 {
				return Packet{}, errors.New("capture: a packet block is too short")
			}
			id := int(r.order.Uint32(body))
			if id >= len(r.ifaces) {
				return Packet{}, fmt.Errorf("capture: a packet is from interface %d, which was not described", id)
			}
			ts := uint64(r.order.Uint32(body[4:]))<<32 | uint64(r.order.Uint32(body[8:]))
			capLen := int(r.order.Uint32(body[12:]))
			if 20+capLen > len(body) {
				return Packet{}, errors.New("capture: a packet is longer than its block")
			}
			p := Packet{Iface: id, Time: time.Unix(0, int64(ts)*r.tsDiv[id]), Data: body[20 : 20+capLen], OrigLen: int(r.order.Uint32(body[16:]))}
			r.options(body[20+pad4(capLen):], func(code uint16, val []byte) {
				if code == optEpbFlags && len(val) == 4 {
					p.Outgoing = r.order.Uint32(val)&3 == 2
				}
			})
			return p, nil
		}
	}
}

// block reads one block of a pcapng file: its type and its body without the framing. A block that is cut off
// is ErrTruncated.
func (r *Reader) block() (uint32, []byte, error) {
	head := make([]byte, 8)
	if n, err := io.ReadFull(r.r, head); err != nil {
		if n == 0 && errors.Is(err, io.EOF) {
			return 0, nil, io.EOF
		}
		return 0, nil, ErrTruncated
	}
	order := r.order
	if order == nil || binary.LittleEndian.Uint32(head) == blockSHB {
		order = binary.LittleEndian // a section header's type reads the same in both orders; its length is fixed below
	}
	typ := order.Uint32(head)
	n := int(order.Uint32(head[4:]))
	if typ == blockSHB {
		// the byte order of the section is in the block; the length is read in the order the magic names
		peek, err := r.r.Peek(4)
		if err != nil {
			return 0, nil, ErrTruncated
		}
		if binary.LittleEndian.Uint32(peek) != byteOrderMagic {
			n = int(binary.BigEndian.Uint32(head[4:]))
		}
	}
	if n < 12 || n%4 != 0 || n > 1<<26 {
		return 0, nil, fmt.Errorf("capture: a block has the length %d", n)
	}
	rest := make([]byte, n-8)
	if _, err := io.ReadFull(r.r, rest); err != nil {
		return 0, nil, ErrTruncated
	}
	return typ, rest[:len(rest)-4], nil
}

func (r *Reader) options(b []byte, f func(code uint16, val []byte)) {
	for len(b) >= 4 {
		code, n := r.order.Uint16(b), int(r.order.Uint16(b[2:]))
		if code == optEnd || 4+n > len(b) {
			return
		}
		f(code, b[4:4+n])
		b = b[4+pad4(n):]
	}
}

// ---- copying complete records

// CopyComplete copies the complete records of a capture file to dst, cutting off a record that is still being
// written at the end of src, and returns the number of bytes copied. With skipHeader a pcap file's global header
// is not copied (the files after the first of a ring); the blocks of a pcapng file are all copied, as a file
// may hold several sections.
func CopyComplete(dst io.Writer, src io.Reader, format Format, skipHeader bool) (int64, error) {
	br := bufio.NewReaderSize(src, 64<<10)
	var total int64
	emit := func(b []byte) error {
		n, err := dst.Write(b)
		total += int64(n)
		return err
	}
	if format == PCAP {
		hdr := make([]byte, pcapHeaderLen)
		if _, err := io.ReadFull(br, hdr); err != nil {
			return total, nil // the header is not there yet
		}
		if !skipHeader {
			if err := emit(hdr); err != nil {
				return total, err
			}
		}
		order := binary.ByteOrder(binary.LittleEndian)
		if binary.LittleEndian.Uint32(hdr) != pcapMagicMicro && binary.LittleEndian.Uint32(hdr) != pcapMagicNano {
			order = binary.BigEndian
		}
		rec := make([]byte, pcapRecordHeader, 64<<10)
		for {
			if _, err := io.ReadFull(br, rec[:pcapRecordHeader]); err != nil {
				return total, nil
			}
			incl := int(order.Uint32(rec[8:]))
			if incl > 1<<24 {
				return total, errors.New("capture: a record is longer than 16 MiB")
			}
			rec = rec[:pcapRecordHeader+incl]
			if _, err := io.ReadFull(br, rec[pcapRecordHeader:]); err != nil {
				return total, nil
			}
			if err := emit(rec); err != nil {
				return total, err
			}
			rec = rec[:pcapRecordHeader]
		}
	}
	head := make([]byte, 8)
	var buf bytes.Buffer
	for {
		if _, err := io.ReadFull(br, head); err != nil {
			return total, nil
		}
		order := binary.ByteOrder(binary.LittleEndian)
		if binary.LittleEndian.Uint32(head) == blockSHB {
			peek, err := br.Peek(4)
			if err != nil {
				return total, nil
			}
			if binary.LittleEndian.Uint32(peek) != byteOrderMagic {
				order = binary.BigEndian
			}
		}
		n := int(order.Uint32(head[4:]))
		if n < 12 || n%4 != 0 || n > 1<<26 {
			return total, fmt.Errorf("capture: a block has the length %d", n)
		}
		buf.Reset()
		buf.Write(head)
		if _, err := io.CopyN(&buf, br, int64(n-8)); err != nil {
			return total, nil
		}
		if err := emit(buf.Bytes()); err != nil {
			return total, err
		}
	}
}
