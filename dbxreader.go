package main

// Lector del formato binario .dbx (Outlook Express).
//
// Adaptado de github.com/csima/dbxconvert (GPL-2.0-or-later), que a su vez
// es un port a Go del dbx-converter original de Ulrich Krebs
// (ukrebs-software.de). Se mantiene la lógica de lectura del formato tal
// cual: es un árbol de índices binario sin documentación oficial, y esta
// implementación ya fue validada contra archivos .dbx reales.

import (
	"encoding/binary"
	"errors"
	"os"
	"strings"
	"time"
)

const (
	dbxTypeEmail   = 0
	dbxTypeFolder  = 1
	dbxTypeOE4     = 2
	dbxTypeUnknown = 3
)

// DBXReader da acceso a los mensajes contenidos en un archivo .dbx.
type DBXReader struct {
	f                 *os.File
	fSize             int64
	dbxType           int
	indexes           []uint32
	subjects          []string
	senders           []string
	senderAddresses   []string
	receivers         []string
	receiverAddresses []string
	receiveDates      []time.Time
	sendDates         []time.Time
}

func (r *DBXReader) init() error {
	r.receiveDates = []time.Time{}
	r.sendDates = []time.Time{}

	signature := [4]uint32{}
	for i := 0; i < 4; i++ {
		_ = binary.Read(r.f, binary.LittleEndian, &signature[i])
	}

	switch {
	case signature[0] == 0xFE12ADCF && signature[1] == 0x6F74FDC5 && signature[2] == 0x11D1E366 && signature[3] == 0xC0004E9A:
		r.dbxType = dbxTypeEmail
	case signature[0] == 0x36464D4A && signature[1] == 0x00010003:
		r.dbxType = dbxTypeOE4
	case signature[0] == 0xFE12ADCF && signature[1] == 0x6F74FDC6 && signature[2] == 0x11D1E366 && signature[3] == 0xC0004E9A:
		r.dbxType = dbxTypeFolder
	default:
		r.dbxType = dbxTypeUnknown
	}

	if r.dbxType == dbxTypeEmail {
		r.readIndexes()
		r.readInfos()
		return nil
	}

	return errors.New("el archivo no tiene formato .dbx de correo de Outlook Express")
}

func (r *DBXReader) readIndexes() {
	const indexPointerOffset = 0xE4
	const itemCountOffset = 0xC4

	var indexPtr uint32
	var itemCount uint32

	_, _ = r.f.Seek(indexPointerOffset, 0)
	_ = binary.Read(r.f, binary.LittleEndian, &indexPtr)

	_, _ = r.f.Seek(itemCountOffset, 0)
	_ = binary.Read(r.f, binary.LittleEndian, &itemCount)

	if itemCount > 0 {
		r.readIndex(int(indexPtr))
	}
}

func (r *DBXReader) readIndex(pos int) {
	if int64(pos) >= r.fSize {
		panic("bad seek")
	}

	var nextTable uint32
	var ptrCount uint8
	var indexCount uint32

	_, _ = r.f.Seek(int64(pos)+8, 0)
	_ = binary.Read(r.f, binary.LittleEndian, &nextTable)
	_, _ = r.f.Seek(5, 1)
	_ = binary.Read(r.f, binary.LittleEndian, &ptrCount)
	_, _ = r.f.Seek(2, 1)
	_ = binary.Read(r.f, binary.LittleEndian, &indexCount)

	if indexCount > 0 {
		r.readIndex(int(nextTable))
	}

	pos += 24

	for i := int64(0); i < int64(ptrCount); i++ {
		_, _ = r.f.Seek(int64(pos), 0)
		var indexPtr uint32
		_ = binary.Read(r.f, binary.LittleEndian, &indexPtr)
		_ = binary.Read(r.f, binary.LittleEndian, &nextTable)
		_ = binary.Read(r.f, binary.LittleEndian, &indexCount)

		r.indexes = append(r.indexes, indexPtr)
		pos += 12

		if indexCount > 0 {
			r.readIndex(int(nextTable))
		}
	}
}

func (r *DBXReader) readInfos() {
	for i := 0; i < r.GetItemCount(); i++ {
		index := uint32(r.GetIndex(i))
		_, _ = r.f.Seek(int64(index)+4, 0)
		var size uint32
		_ = binary.Read(r.f, binary.LittleEndian, &size)
		_, _ = r.f.Seek(2, 1)
		var count uint8
		_ = binary.Read(r.f, binary.LittleEndian, &count)
		_, _ = r.f.Seek(1, 1)

		var sender, senderAddress, receiver, receiverAddress, subject, receiverDate, sendDate bool

		pos := index + 12

		for j := uint8(0); j < count; j++ {
			_, _ = r.f.Seek(int64(pos), 0)
			var tp uint8
			_ = binary.Read(r.f, binary.LittleEndian, &tp)

			b := []byte{}
			var bt byte
			for n := 0; n < 3; n++ {
				_ = binary.Read(r.f, binary.LittleEndian, &bt)
				b = append(b, bt)
			}
			b = append(b, 0)
			value := binary.LittleEndian.Uint32(b)

			offset := uint32(int(index) + 12 + 4*int(count) + int(value))
			switch tp {
			case 0x02:
				r.sendDates = append(r.sendDates, r.readDate(int(offset)))
				sendDate = true
			case 0x0E:
				r.senderAddresses = append(r.senderAddresses, r.readString(int(offset)))
				senderAddress = true
			case 0x0D:
				r.senders = append(r.senders, r.readString(int(offset)))
				sender = true
			case 0x08:
				r.subjects = append(r.subjects, r.readString(int(offset)))
				subject = true
			case 0x12:
				r.receiveDates = append(r.receiveDates, r.readDate(int(offset)))
				receiverDate = true
			case 0x13:
				r.receivers = append(r.receivers, r.readString(int(offset)))
				receiver = true
			case 0x14:
				r.receiverAddresses = append(r.receiverAddresses, r.readString(int(offset)))
				receiverAddress = true
			}
			pos += 4
		}

		if !sender {
			r.senders = append(r.senders, "")
		}
		if !senderAddress {
			r.senderAddresses = append(r.senderAddresses, "")
		}
		if !receiver {
			r.receivers = append(r.receivers, "")
		}
		if !receiverAddress {
			r.receiverAddresses = append(r.receiverAddresses, "")
		}
		if !subject {
			r.subjects = append(r.subjects, "")
		}
		if !receiverDate {
			r.receiveDates = append(r.receiveDates, time.Time{})
		}
		if !sendDate {
			r.sendDates = append(r.sendDates, time.Time{})
		}
	}
}

func (r *DBXReader) readString(offset int) (s string) {
	if int64(offset) >= r.fSize {
		panic("bad seek")
	}
	_, _ = r.f.Seek(int64(offset), 0)
	var c []byte
	var ch byte

	for {
		_ = binary.Read(r.f, binary.LittleEndian, &ch)
		if ch != 0x00 {
			c = append(c, ch)
			continue
		}
		c = append(c, 0x00)
		s += string(c)
		if len(c) != 256 {
			break
		}
		c = []byte{}
	}
	return
}

func (r *DBXReader) readDate(offset int) time.Time {
	if int64(offset) >= r.fSize {
		panic("bad seek")
	}
	_, _ = r.f.Seek(int64(offset), 0)
	var fileTime int64
	_ = binary.Read(r.f, binary.LittleEndian, &fileTime)
	if fileTime < 0 {
		return time.Time{}
	}
	const ticksPerSecond int64 = 10000000
	const epochDifference int64 = 11644473600
	temp := fileTime/ticksPerSecond - epochDifference
	return time.Unix(temp, 0)
}

// Open abre un archivo .dbx por su ruta.
func (r *DBXReader) Open(fn string) error {
	var err error
	r.f, err = os.Open(fn)
	if err != nil {
		return err
	}

	stat, _ := r.f.Stat()
	r.fSize = stat.Size()

	return r.init()
}

// Close cierra el archivo .dbx.
func (r *DBXReader) Close() error {
	return r.f.Close()
}

// GetType devuelve el subtipo de archivo dbx detectado.
func (r *DBXReader) GetType() int {
	return r.dbxType
}

// GetItemCount devuelve la cantidad de mensajes en el archivo.
func (r *DBXReader) GetItemCount() int {
	return len(r.indexes)
}

// GetIndex devuelve el offset del mensaje N dentro del archivo.
func (r *DBXReader) GetIndex(i int) int {
	return int(r.indexes[uint32(i)])
}

// GetSender devuelve el nombre del remitente del mensaje N.
func (r *DBXReader) GetSender(msgNumber int) string {
	return strings.Trim(r.senders[msgNumber], "\x00")
}

// GetSenderAddress devuelve la dirección del remitente del mensaje N.
func (r *DBXReader) GetSenderAddress(msgNumber int) string {
	return strings.Trim(r.senderAddresses[msgNumber], "\x00")
}

// GetSubject devuelve el asunto del mensaje N.
func (r *DBXReader) GetSubject(msgNumber int) string {
	return strings.Trim(r.subjects[msgNumber], "\x00")
}

// GetSendDate devuelve la fecha de envío del mensaje N.
func (r *DBXReader) GetSendDate(msgNumber int) time.Time {
	return r.sendDates[msgNumber]
}

// GetReceiveDate devuelve la fecha de recepción del mensaje N.
func (r *DBXReader) GetReceiveDate(msgNumber int) time.Time {
	return r.receiveDates[msgNumber]
}

// GetMessage devuelve el mensaje N completo (headers + cuerpo) en formato
// MIME crudo, tal como estaba almacenado dentro del .dbx.
func (r *DBXReader) GetMessage(msgNumber int) string {
	index := r.GetIndex(msgNumber)
	var size uint32
	var count uint8

	_, _ = r.f.Seek(int64(index)+4, 0)
	_ = binary.Read(r.f, binary.LittleEndian, &size)
	_, _ = r.f.Seek(2, 1)
	_ = binary.Read(r.f, binary.LittleEndian, &count)
	_, _ = r.f.Seek(1, 1)

	var msgOffset, msgOffsetPtr uint32

	for i := uint8(0); i < count; i++ {
		var t uint8
		_ = binary.Read(r.f, binary.LittleEndian, &t)
		b := []byte{}
		var bt byte
		for n := 0; n < 3; n++ {
			_ = binary.Read(r.f, binary.LittleEndian, &bt)
			b = append(b, bt)
		}
		b = append(b, 0)
		value := binary.LittleEndian.Uint32(b)
		if t == 0x84 {
			msgOffset = value
			break
		}
		if t == 0x04 {
			msgOffsetPtr = uint32(int(index) + 12 + int(value) + 4*int(count))
			break
		}
	}

	if msgOffset == 0 && msgOffsetPtr != 0 {
		_, _ = r.f.Seek(int64(msgOffsetPtr), 0)
		_ = binary.Read(r.f, binary.LittleEndian, &msgOffset)
	}

	var pl []byte
	i2 := msgOffset
	var oldIndex uint32
	for i2 != 0 {
		if int64(i2) >= r.fSize {
			panic("bad seek")
		}
		if i2 == oldIndex {
			panic("loop detectado en la cadena de bloques del mensaje")
		}
		oldIndex = i2

		_, _ = r.f.Seek(int64(i2)+8, 0)
		var blockSize uint16
		_ = binary.Read(r.f, binary.LittleEndian, &blockSize)
		_, _ = r.f.Seek(2, 1)
		_ = binary.Read(r.f, binary.LittleEndian, &i2)

		bbuf := make([]byte, blockSize)
		_, _ = r.f.Read(bbuf)
		pl = append(pl, bbuf...)
	}

	return string(pl)
}
