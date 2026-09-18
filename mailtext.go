package main

// Convierte un mensaje de correo en formato MIME crudo (tal como lo
// devuelve DBXReader.GetMessage) en un bloque de texto legible: cabecera
// simplificada + cuerpo en texto plano. Soporta mensajes multipart,
// distintos Content-Transfer-Encoding y charsets no-UTF8 típicos de
// correos viejos (ISO-8859-1, Windows-1252, etc). Los adjuntos no se
// incluyen: solo se deja constancia de su nombre.

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"html"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"golang.org/x/text/encoding/htmlindex"
)

type headerGetter interface {
	Get(string) string
}

type extracted struct {
	plain       []string
	html        []string
	attachments []string
}

func charsetReader(charset string, input io.Reader) (io.Reader, error) {
	charset = strings.ToLower(strings.TrimSpace(charset))
	if charset == "" || charset == "utf-8" || charset == "utf8" || charset == "us-ascii" || charset == "ascii" {
		return input, nil
	}
	enc, err := htmlindex.Get(charset)
	if err != nil {
		// Charset desconocido: devolvemos el contenido tal cual antes que
		// abortar la conversión de todo el mensaje por esto.
		return input, nil
	}
	return enc.NewDecoder().Reader(input), nil
}

func decodeCharsetBytes(charset string, data []byte) string {
	r, err := charsetReader(charset, bytes.NewReader(data))
	if err != nil {
		return string(data)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		return string(data)
	}
	return string(out)
}

var wordDecoder = &mime.WordDecoder{CharsetReader: charsetReader}

func decodeHeaderValue(v string) string {
	if v == "" {
		return v
	}
	d, err := wordDecoder.DecodeHeader(v)
	if err != nil {
		return v
	}
	return d
}

func decodeTransferEncoding(enc string, r io.Reader) io.Reader {
	switch strings.ToLower(strings.TrimSpace(enc)) {
	case "quoted-printable":
		return quotedprintable.NewReader(r)
	case "base64":
		return base64.NewDecoder(base64.StdEncoding, r)
	default:
		return r
	}
}

var (
	scriptStyleRe = regexp.MustCompile(`(?is)<(script|style)[^>]*>.*?</(script|style)>`)
	brRe          = regexp.MustCompile(`(?i)<br\s*/?>`)
	closeParaRe   = regexp.MustCompile(`(?i)</p>`)
	tagRe         = regexp.MustCompile(`(?s)<[^>]*>`)
	blankLinesRe  = regexp.MustCompile(`\n{3,}`)
)

func htmlToText(h string) string {
	h = scriptStyleRe.ReplaceAllString(h, "")
	h = brRe.ReplaceAllString(h, "\n")
	h = closeParaRe.ReplaceAllString(h, "\n\n")
	h = tagRe.ReplaceAllString(h, "")
	h = html.UnescapeString(h)
	h = blankLinesRe.ReplaceAllString(h, "\n\n")
	return strings.TrimSpace(h)
}

// processPart recorre (recursivamente si es multipart) las partes de un
// mensaje y acumula texto plano, HTML y nombres de adjuntos encontrados.
func processPart(h headerGetter, body io.Reader, out *extracted, depth int) {
	if depth > 15 {
		return // corta cualquier anidamiento multipart patológico
	}

	mediaType, params, err := mime.ParseMediaType(h.Get("Content-Type"))
	if err != nil || mediaType == "" {
		mediaType = "text/plain"
		params = map[string]string{}
	}

	dType, dParams, _ := mime.ParseMediaType(h.Get("Content-Disposition"))
	filename := decodeHeaderValue(dParams["filename"])
	if filename == "" {
		filename = decodeHeaderValue(params["name"])
	}
	isAttachment := strings.EqualFold(dType, "attachment") ||
		(filename != "" && !strings.HasPrefix(mediaType, "text/") && !strings.HasPrefix(mediaType, "multipart/"))

	if strings.HasPrefix(mediaType, "multipart/") {
		boundary := params["boundary"]
		if boundary == "" {
			return
		}
		mr := multipart.NewReader(body, boundary)
		for {
			p, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				break
			}
			processPart(p.Header, p, out, depth+1)
		}
		return
	}

	if isAttachment {
		if filename == "" {
			filename = "sin_nombre"
		}
		out.attachments = append(out.attachments, filename)
		return
	}

	raw, err := io.ReadAll(decodeTransferEncoding(h.Get("Content-Transfer-Encoding"), body))
	if err != nil {
		return
	}
	text := decodeCharsetBytes(params["charset"], raw)

	switch {
	case strings.HasPrefix(mediaType, "text/plain"):
		out.plain = append(out.plain, text)
	case strings.HasPrefix(mediaType, "text/html"):
		out.html = append(out.html, text)
	default:
		if filename != "" {
			out.attachments = append(out.attachments, filename)
		}
	}
}

func splitHeadersBody(raw string) (string, string) {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	if idx := strings.Index(raw, "\n\n"); idx != -1 {
		return raw[:idx], raw[idx+2:]
	}
	return raw, ""
}

func parseHeadersLoose(h string) map[string]string {
	out := map[string]string{}
	lastKey := ""
	for _, line := range strings.Split(h, "\n") {
		if line == "" {
			continue
		}
		if (strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")) && lastKey != "" {
			out[lastKey] += " " + strings.TrimSpace(line)
			continue
		}
		if idx := strings.Index(line, ":"); idx != -1 {
			key := strings.ToLower(strings.TrimSpace(line[:idx]))
			out[key] = strings.TrimSpace(line[idx+1:])
			lastKey = key
		}
	}
	return out
}

type looseHeader map[string]string

func (h looseHeader) Get(key string) string {
	return h[strings.ToLower(key)]
}

// RenderMessageText arma el .txt final de un mensaje. fallbackSender,
// fallbackSubject y fallbackDate vienen del propio índice del .dbx y se
// usan solo si el mensaje MIME no trae esos datos en sus cabeceras.
func renderMessageText(raw, fallbackSender, fallbackSubject string, fallbackDate time.Time) (text string, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("panic al decodificar el mensaje: %v", p)
		}
	}()

	out := &extracted{}
	var from, to, subject, date string

	msg, parseErr := mail.ReadMessage(strings.NewReader(raw))
	if parseErr == nil {
		from = decodeHeaderValue(msg.Header.Get("From"))
		to = decodeHeaderValue(msg.Header.Get("To"))
		subject = decodeHeaderValue(msg.Header.Get("Subject"))
		date = msg.Header.Get("Date")
		processPart(msg.Header, msg.Body, out, 0)
	} else {
		h, b := splitHeadersBody(raw)
		hdr := looseHeader(parseHeadersLoose(h))
		from = decodeHeaderValue(hdr.Get("From"))
		to = decodeHeaderValue(hdr.Get("To"))
		subject = decodeHeaderValue(hdr.Get("Subject"))
		date = hdr.Get("Date")
		if strings.HasPrefix(strings.ToLower(hdr.Get("Content-Type")), "multipart/") {
			processPart(hdr, strings.NewReader(b), out, 0)
		} else {
			decoded := decodeTransferEncoding(hdr.Get("Content-Transfer-Encoding"), strings.NewReader(b))
			data, _ := io.ReadAll(decoded)
			out.plain = append(out.plain, decodeCharsetBytes(mediaTypeCharset(hdr.Get("Content-Type")), data))
		}
	}

	if from == "" {
		from = fallbackSender
	}
	if subject == "" {
		subject = fallbackSubject
	}
	if date == "" && !fallbackDate.IsZero() {
		date = fallbackDate.Format("2006-01-02 15:04:05")
	}

	bodyText := strings.TrimSpace(strings.Join(out.plain, "\n\n"))
	if bodyText == "" && len(out.html) > 0 {
		parts := make([]string, 0, len(out.html))
		for _, h := range out.html {
			parts = append(parts, htmlToText(h))
		}
		bodyText = strings.TrimSpace(strings.Join(parts, "\n\n"))
	}
	if bodyText == "" {
		bodyText = "(este mensaje no tenía contenido de texto legible)"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "De: %s\n", nonEmpty(from, "(desconocido)"))
	if to != "" {
		fmt.Fprintf(&b, "Para: %s\n", to)
	}
	fmt.Fprintf(&b, "Fecha: %s\n", nonEmpty(date, "(desconocida)"))
	fmt.Fprintf(&b, "Asunto: %s\n", nonEmpty(subject, "(sin asunto)"))
	if len(out.attachments) > 0 {
		fmt.Fprintf(&b, "Adjuntos omitidos: %s\n", strings.Join(out.attachments, ", "))
	}
	b.WriteString("\n")
	b.WriteString(bodyText)
	b.WriteString("\n")

	return b.String(), nil
}

func mediaTypeCharset(contentType string) string {
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return ""
	}
	return params["charset"]
}

func nonEmpty(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}
