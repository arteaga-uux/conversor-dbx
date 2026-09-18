package main

import (
	"strings"
	"testing"
	"time"
)

// Los correos viejos de Outlook Express casi siempre vienen en
// ISO-8859-1/Windows-1252 con Content-Transfer-Encoding: quoted-printable.
// Este test cubre justamente ese caso, con acentos y eñes españolas, para
// no volver a romperlo sin darnos cuenta.
func TestRenderMessageText_ISO88591QuotedPrintable(t *testing.T) {
	raw := "From: =?ISO-8859-1?Q?Ram=F3n_Arteaga?= <ramon@example.com>\r\n" +
		"To: companera@example.com\r\n" +
		"Subject: =?ISO-8859-1?Q?Informe_de_gesti=F3n?=\r\n" +
		"Date: Wed, 12 Mar 2003 10:15:00 +0000\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=ISO-8859-1\r\n" +
		"Content-Transfer-Encoding: quoted-printable\r\n" +
		"\r\n" +
		"Hola compa=F1era,\r\n\r\n" +
		"=BFC=F3mo est=E1s? Ac=E1 va el informe de gesti=F3n con acentos: educaci=F3=\r\n" +
		"n, a=F1o, ma=F1ana.\r\n\r\n" +
		"Saludos, Ram=F3n"

	text, err := renderMessageText(raw, "", "", time.Time{})
	if err != nil {
		t.Fatalf("renderMessageText devolvió error: %v", err)
	}

	for _, want := range []string{
		"De: Ramón Arteaga <ramon@example.com>",
		"Asunto: Informe de gestión",
		"compañera",
		"¿Cómo estás?",
		"educación",
		"año",
		"mañana",
		"Saludos, Ramón",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("esperaba encontrar %q en el texto decodificado, no apareció.\n--- salida ---\n%s", want, text)
		}
	}
}

func TestRenderMessageText_MultipartAlternativeSkipsAttachment(t *testing.T) {
	raw := "From: Alguien <alguien@example.com>\r\n" +
		"To: destino@example.com\r\n" +
		"Subject: Con adjunto\r\n" +
		"Date: Wed, 12 Mar 2003 10:15:00 +0000\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=\"XYZ\"\r\n" +
		"\r\n" +
		"--XYZ\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"\r\n" +
		"Cuerpo del mensaje en texto plano.\r\n" +
		"--XYZ\r\n" +
		"Content-Type: application/pdf; name=\"factura.pdf\"\r\n" +
		"Content-Disposition: attachment; filename=\"factura.pdf\"\r\n" +
		"Content-Transfer-Encoding: base64\r\n" +
		"\r\n" +
		"JVBERi0xLjQK\r\n" +
		"--XYZ--\r\n"

	text, err := renderMessageText(raw, "", "", time.Time{})
	if err != nil {
		t.Fatalf("renderMessageText devolvió error: %v", err)
	}
	if !strings.Contains(text, "Cuerpo del mensaje en texto plano.") {
		t.Errorf("no se encontró el cuerpo de texto plano en la salida:\n%s", text)
	}
	if !strings.Contains(text, "Adjuntos omitidos: factura.pdf") {
		t.Errorf("no se listó el adjunto omitido en la salida:\n%s", text)
	}
	if strings.Contains(text, "JVBERi0xLjQK") {
		t.Errorf("el contenido binario del adjunto no debería aparecer en el texto:\n%s", text)
	}
}
