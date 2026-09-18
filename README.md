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

## Requisitos en el VPS

- Docker (para el contenedor de la app).
- nginx + certbot (para exponer `conversor.revistavitul.com` con HTTPS).

Este proyecto **no trae su propio reverse proxy**: el `app` solo publica
`127.0.0.1:8110` (loopback), y es el nginx que ya corre en el VPS — el mismo
que sirve los demás sitios del servidor — el que expone eso al público con
un vhost nuevo. Esto evita pisar los puertos 80/443 que ya usa nginx (y
cualquier otro sitio existente en el mismo VPS).

## Deploy

Se puede desplegar desde el **Administrador de Docker** de Hostinger
("Componer" → "Componer desde URL", apuntando a la URL cruda de
`docker-compose.yml` de este repo en GitHub) o a mano por SSH con
`docker compose up -d --build`. En ambos casos hace falta cargar las
variables de entorno (`AUTH_USER`, `AUTH_PASS`, `MAX_UPLOAD_MB`) — copiá
`.env.example` como referencia y NO dejes `AUTH_USER`/`AUTH_PASS` vacíos, o
la página queda abierta a cualquiera que tenga la URL.

Una vez que el contenedor está corriendo en `127.0.0.1:8110`, falta el vhost
de nginx (agregar, no reemplazar nada de lo que ya había):

```bash
cat > /etc/nginx/sites-available/conversor-dbx <<'EOF'
server {
    server_name conversor.revistavitul.com;

    client_max_body_size 2100m;

    location / {
        proxy_pass http://127.0.0.1:8110;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_read_timeout 600s;
    }

    listen 80;
}
EOF
ln -s /etc/nginx/sites-available/conversor-dbx /etc/nginx/sites-enabled/conversor-dbx
nginx -t && systemctl reload nginx
certbot --nginx -d conversor.revistavitul.com
```

`certbot` reescribe ese archivo para agregar HTTPS (igual que hace con los
otros sitios del VPS), y de ahí en adelante renueva el certificado solo.

Para actualizar después de un cambio de código (desde el Administrador de
Docker de Hostinger: **Eliminar** el proyecto y volver a **Componer desde
URL** — el editor de "Actualizar" no vuelve a construir la imagen. Por SSH
alcanza con):

```bash
git pull
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
