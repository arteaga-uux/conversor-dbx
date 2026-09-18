# Conversor de .dbx a texto

Página web para convertir archivos `.dbx` de Outlook Express (uno o varios)
en archivos `.txt` legibles. Está pensada para correr en un VPS propio,
detrás de `conversor.revistavitul.com`.

## Cómo funciona

- El servidor es un único binario en Go (`main.go`, `dbxreader.go`,
  `mailtext.go`), sin base de datos ni dependencias externas más que
  `golang.org/x/text` (para decodificar charsets viejos tipo ISO-8859-1 /
  Windows-1252).
- `dbxreader.go` lee el formato binario `.dbx` — está adaptado de
  [csima/dbxconvert](https://github.com/csima/dbxconvert) (GPL-2.0-or-later),
  que ya tiene resuelta la ingeniería inversa del formato.
- Por cada `.dbx` subido, se extrae cada mensaje, se decodifica el MIME
  (multipart, quoted-printable/base64, charset) y se genera un `.txt` con
  remitente, fecha, asunto y cuerpo. Los adjuntos NO se incluyen, solo se
  deja constancia del nombre del archivo adjunto.
- Todos los `.txt` de una subida se devuelven juntos en un `.zip`. Nada
  queda guardado en el servidor: los archivos temporales se borran apenas
  termina cada conversión.
- La página está protegida con autenticación básica (usuario/clave del
  navegador), definida en `.env`.

## Requisitos en el VPS (Hostinger)

Solo necesitás Docker. Si el VPS no lo tiene:

```bash
curl -fsSL https://get.docker.com | sh
```

## Deploy

1. Copiá esta carpeta al VPS (por ejemplo con `scp -r` o `git clone` si la
   subís a un repo propio).
2. Copiá `.env.example` a `.env` y completá `AUTH_USER` / `AUTH_PASS` con
   una clave real (evitá dejarlos vacíos: sin eso la página queda abierta
   a cualquiera que tenga la URL).

   ```bash
   cp .env.example .env
   nano .env
   ```

3. En tu proveedor de DNS, agregá un registro **A** para
   `conversor.revistavitul.com` apuntando a la IP del VPS. Esto tiene que
   estar propagado antes del paso 4, porque Caddy pide el certificado TLS
   automáticamente la primera vez que alguien entra.
4. Levantá todo:

   ```bash
   docker compose up -d --build
   ```

   Esto levanta dos contenedores: `app` (el conversor) y `caddy` (que
   sirve `https://conversor.revistavitul.com` con certificado automático
   de Let's Encrypt y le pasa el tráfico a `app`).
5. Entrá a `https://conversor.revistavitul.com`, te va a pedir el usuario y
   clave que pusiste en `.env`, y ya podés subir archivos `.dbx`.

Para actualizar después de un cambio de código:

```bash
git pull   # o volver a copiar los archivos
docker compose up -d --build
```

Para ver logs:

```bash
docker compose logs -f app
```

## Desarrollo / probar en tu máquina

Necesitás Go 1.22+ instalado.

```bash
go run .
```

Por defecto escucha en `http://localhost:8080` sin clave (si no definís
`AUTH_USER`/`AUTH_PASS` como variables de entorno, el servidor lo avisa por
consola y queda abierto — solo para probar en local).

Correr los tests:

```bash
go test ./...
```

## Límites conocidos

- Pensado para uso personal/ocasional (una persona subiendo sus propios
  archivos), no para tráfico público masivo.
- El límite de subida por defecto es 2 GB por conversión (variable
  `MAX_UPLOAD_MB` en `.env`).
- Si un `.dbx` está corrupto o algún mensaje puntual no se puede leer, el
  `.zip` igual se genera: en vez de ese mensaje vas a encontrar un `.txt`
  o `ERROR.txt` explicando qué pasó, en lugar de que falle toda la
  conversión.
- El formato `.dbx` es binario y no tiene documentación oficial de
  Microsoft; esta implementación fue probada contra un archivo de ejemplo
  real y contra mensajes sintéticos con distintos charsets, pero si algún
  `.dbx` particular da problemas, lo ideal es mandarlo para ajustar el
  parser puntual a ese caso.

## Licencia de terceros

`dbxreader.go` está adaptado de
[csima/dbxconvert](https://github.com/csima/dbxconvert), licenciado
GPL-2.0-or-later.
