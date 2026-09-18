package main

import (
	"archive/zip"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

//go:embed web/index.html
var webFS embed.FS

var history *historyStore

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	port := getEnv("PORT", "8080")
	authUser := os.Getenv("AUTH_USER")
	authPass := os.Getenv("AUTH_PASS")
	maxUploadMB, err := strconv.Atoi(getEnv("MAX_UPLOAD_MB", "2048"))
	if err != nil || maxUploadMB <= 0 {
		maxUploadMB = 2048
	}
	maxUploadBytes := int64(maxUploadMB) << 20

	if authUser == "" || authPass == "" {
		log.Println("AVISO: AUTH_USER / AUTH_PASS no están definidos. El conversor va a quedar accesible SIN contraseña para cualquiera que tenga la URL.")
	}

	indexPage, err := webFS.ReadFile("web/index.html")
	if err != nil {
		log.Fatalf("no se pudo cargar la página embebida: %v", err)
	}

	history = newHistoryStore(getEnv("HISTORY_PATH", "/data/historial.json"))

	mux := http.NewServeMux()
	mux.HandleFunc("/", handleIndex(indexPage))
	mux.HandleFunc("/convert", handleConvert(maxUploadBytes))
	mux.HandleFunc("/historial", handleHistorial)

	handler := basicAuth(mux, authUser, authPass)

	srv := &http.Server{
		Addr:        ":" + port,
		Handler:     handler,
		IdleTimeout: 120 * time.Second,
		// Sin ReadTimeout/WriteTimeout: los .dbx grandes pueden tardar en subir.
	}

	log.Printf("Conversor DBX escuchando en :%s (límite de subida: %d MB)", port, maxUploadMB)
	log.Fatal(srv.ListenAndServe())
}

func basicAuth(next http.Handler, user, pass string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user == "" && pass == "" {
			next.ServeHTTP(w, r)
			return
		}
		u, p, ok := r.BasicAuth()
		if !ok ||
			subtle.ConstantTimeCompare([]byte(u), []byte(user)) != 1 ||
			subtle.ConstantTimeCompare([]byte(p), []byte(pass)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="Conversor DBX"`)
			http.Error(w, "No autorizado", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func handleIndex(page []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "Método no permitido", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(page)
	}
}

func handleHistorial(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Método no permitido", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(history.List())
}

func handleConvert(maxUploadBytes int64) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Método no permitido", http.StatusMethodNotAllowed)
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			http.Error(w, "El archivo es demasiado grande o la subida falló: "+err.Error(), http.StatusBadRequest)
			return
		}
		defer func() {
			if r.MultipartForm != nil {
				_ = r.MultipartForm.RemoveAll()
			}
		}()

		files := r.MultipartForm.File["files"]
		if len(files) == 0 {
			http.Error(w, "No se recibió ningún archivo .dbx", http.StatusBadRequest)
			return
		}

		tempDir, err := os.MkdirTemp("", "dbxconv-*")
		if err != nil {
			http.Error(w, "No se pudo crear un directorio temporal en el servidor", http.StatusInternalServerError)
			return
		}
		defer os.RemoveAll(tempDir)

		zipPath := filepath.Join(tempDir, "salida.zip")
		zipFile, err := os.Create(zipPath)
		if err != nil {
			http.Error(w, "No se pudo generar el archivo de salida", http.StatusInternalServerError)
			return
		}
		zw := zip.NewWriter(zipFile)

		usedFolderNames := map[string]int{}
		for idx, fh := range files {
			convertOneDBX(zw, tempDir, idx, fh, usedFolderNames)
		}

		if err := zw.Close(); err != nil {
			_ = zipFile.Close()
			http.Error(w, "No se pudo cerrar el archivo de salida", http.StatusInternalServerError)
			return
		}
		_ = zipFile.Close()

		out, err := os.Open(zipPath)
		if err != nil {
			http.Error(w, "No se pudo leer el archivo de salida", http.StatusInternalServerError)
			return
		}
		defer out.Close()

		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="correos-en-texto.zip"`)
		_, _ = io.Copy(w, out)
	}
}

// convertOneDBX procesa un .dbx subido y escribe sus mensajes como .txt
// dentro de una carpeta propia en el zip de salida. Nunca deja que un
// archivo corrupto o un mensaje dañado interrumpa el resto de la conversión:
// cualquier problema queda registrado como un ERROR.txt o un .txt de aviso.
func convertOneDBX(zw *zip.Writer, tempDir string, idx int, fh *multipart.FileHeader, usedFolderNames map[string]int) {
	baseName := sanitizeFilename(strings.TrimSuffix(filepath.Base(fh.Filename), filepath.Ext(fh.Filename)))
	if baseName == "" {
		baseName = "correo"
	}
	folderName := baseName
	if n, exists := usedFolderNames[baseName]; exists {
		usedFolderNames[baseName] = n + 1
		folderName = fmt.Sprintf("%s (%d)", baseName, n+1)
	} else {
		usedFolderNames[baseName] = 1
	}

	src, err := fh.Open()
	if err != nil {
		writeZipError(zw, folderName, fmt.Sprintf("No se pudo abrir el archivo subido: %v", err))
		return
	}
	defer src.Close()

	dbxPath := filepath.Join(tempDir, fmt.Sprintf("in-%d.dbx", idx))
	dst, err := os.Create(dbxPath)
	if err != nil {
		writeZipError(zw, folderName, fmt.Sprintf("No se pudo guardar el archivo temporalmente: %v", err))
		return
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		writeZipError(zw, folderName, fmt.Sprintf("No se pudo leer el archivo subido completo: %v", err))
		return
	}
	_ = dst.Close()

	dbx, err := safeOpenDBX(dbxPath)
	if err != nil || dbx == nil {
		writeZipError(zw, folderName, fmt.Sprintf("\"%s\" no se pudo leer como .dbx de Outlook Express: %v", fh.Filename, err))
		return
	}
	defer dbx.Close()

	count := dbx.GetItemCount()
	if count == 0 {
		writeZipError(zw, folderName, "Este archivo .dbx no contiene mensajes (puede ser un índice de carpetas, no un buzón de correo).")
		return
	}

	history.Append(fh.Filename, count)

	for i := 0; i < count; i++ {
		raw, sender, subject, sendDate, err := safeExtractMessage(dbx, i)
		var text string
		if err != nil {
			text = fmt.Sprintf("No se pudo leer este mensaje (registro %d dañado en el .dbx original): %v\n", i+1, err)
		} else {
			text, err = renderMessageText(raw, sender, subject, sendDate)
			if err != nil {
				text = fmt.Sprintf("No se pudo decodificar completamente este mensaje: %v\n\n--- contenido original ---\n%s", err, raw)
			}
		}

		entryName := fmt.Sprintf("%s/%04d - %s - %s.txt", folderName, i+1, dateSlug(sendDate), sanitizeFilename(subject))
		fw, err := zw.Create(entryName)
		if err != nil {
			continue
		}
		_, _ = io.WriteString(fw, text)
	}
}

func writeZipError(zw *zip.Writer, folderName, message string) {
	fw, err := zw.Create(fmt.Sprintf("%s/ERROR.txt", folderName))
	if err != nil {
		return
	}
	_, _ = io.WriteString(fw, message+"\n")
}

// safeOpenDBX abre y parsea el índice de un .dbx protegiéndose de los
// panics que puede tirar el parser binario ante un archivo corrupto o con
// un offset inválido, y los convierte en un error normal.
func safeOpenDBX(path string) (dbx *DBXReader, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("%v", p)
			dbx = nil
		}
	}()
	d := &DBXReader{}
	if openErr := d.Open(path); openErr != nil {
		return nil, openErr
	}
	return d, nil
}

// safeExtractMessage lee un mensaje puntual del .dbx protegiéndose de
// panics del parser binario, para que un solo registro dañado no tire
// abajo la conversión de los demás mensajes del buzón.
func safeExtractMessage(dbx *DBXReader, i int) (raw, sender, subject string, sendDate time.Time, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("%v", p)
		}
	}()
	raw = dbx.GetMessage(i)
	sender = dbx.GetSender(i)
	subject = dbx.GetSubject(i)
	sendDate = dbx.GetSendDate(i)
	if sendDate.IsZero() {
		sendDate = dbx.GetReceiveDate(i)
	}
	return
}

var unsafeNameRe = regexp.MustCompile(`[\\/:*?"<>|\x00-\x1F]`)

func sanitizeFilename(s string) string {
	s = unsafeNameRe.ReplaceAllString(s, " ")
	s = strings.Join(strings.Fields(s), " ")
	s = strings.TrimSpace(s)
	if s == "" {
		return "sin_nombre"
	}
	const maxLen = 80
	if len(s) > maxLen {
		s = strings.TrimSpace(s[:maxLen])
	}
	return s
}

func dateSlug(t time.Time) string {
	if t.IsZero() {
		return "sin-fecha"
	}
	return t.Format("2006-01-02")
}
