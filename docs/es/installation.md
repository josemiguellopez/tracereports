# Instalación

🌐 [English](../en/installation.md) · **Español**

TraceReports es un único binario: la interfaz web va embebida y los datos se guardan en una base
SQLite local. No necesita base de datos externa, Node ni CGO.

## Opción 1: Docker (recomendada para servidores)

```bash
git clone https://github.com/josemiguellopez/tracereports.git && cd tracereports
cp -n .env.example .env       # solo si aún no tienes .env (-n no lo pisa)
docker compose up -d
```

- UI: <http://localhost:8080>
- Datos: volumen `tracereports-data` (base de datos + capturas). Respaldo: ver [Backup y restauración](#backup-y-restauración).
- Guía completa (uso diario, importar datos, servidor con HTTPS, problemas frecuentes): [Docker](docker.md).
- Logs: `docker compose logs -f`

Sin compose:

```bash
docker build -t tracereports .
docker run -d -p 8080:8080 -v tracereports-data:/data --env-file .env tracereports
```

La imagen corre como usuario sin privilegios sobre una base *distroless* (sin shell).

## Opción 2: binario con Go

Requiere Go 1.26 o superior.

```bash
go build -o tracereports ./cmd        # Windows: -o tracereports.exe
./tracereports                        # escucha en :8080 y guarda datos en ./data
```

Si en esa carpeta hay un archivo `.env`, el servidor lo lee al arrancar (`cp -n .env.example .env`, que no reemplaza un `.env` existente).

Para otro sistema operativo: `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o tracereports ./cmd`.

Durante el desarrollo puedes usar `go run ./cmd`. La UI se compila dentro del binario, así que si
cambias archivos de `web/` tienes que reiniciar el servidor.

## Primer uso

1. Abre <http://localhost:8080>. Sin ejecuciones verás una pantalla vacía.
2. Corre un ejemplo:

   ```bash
   pip install pytest pytest-playwright ./client/python
   playwright install chromium        # o, con Chrome instalado: --browser-channel chrome
   pytest examples/pytest-playwright --tracereports
   ```

3. Al terminar, la consola muestra el link a la ejecución.

## Datos

| Ruta | Contenido |
|---|---|
| `DATA_DIR/tracereports.db` | Ejecuciones, tests, pasos, red, diagnósticos, ajustes (SQLite). |
| `DATA_DIR/screenshots/` | Capturas de pantalla |

`DATA_DIR` es `./data` por defecto (`/data` en Docker). Para empezar de cero, detén el servidor y
borra esa carpeta. Las tablas nuevas de cada versión se crean o migran solas al arrancar.

## Backup y restauración

La base usa SQLite en modo WAL: con el servidor en marcha, parte de lo último guardado vive en
`tracereports.db-wal`. **No copies solo el archivo `.db` con el servidor encendido**: la copia puede
quedar sin lo más reciente. Usa el backup en línea de SQLite, que es consistente aunque el servidor
siga recibiendo resultados, y luego copia las capturas (en ese orden: así ninguna captura
referenciada falta).

**Docker** (el volumen es `tracereports-data`; si usas otro, búscalo con `docker volume ls`):

```bash
docker volume create tracereports-backup
docker run --rm -v tracereports-data:/data -v tracereports-backup:/backup alpine sh -c \
  "apk add -q sqlite && sqlite3 /data/tracereports.db '.backup /backup/tracereports.db' \
   && cp -a /data/screenshots /backup/ && chown -R 65532:65532 /backup"
```

El `chown` deja los archivos del usuario sin privilegios de la imagen, que
si no podría leerlos pero no escribir en ellos.

**Restaurar**: arranca el servidor apuntando al volumen del backup (o copia su contenido a un
volumen nuevo), por ejemplo `docker run -v tracereports-backup:/data ... tracereports`. Antes de
restaurar en producción, comprueba la copia:
`sqlite3 tracereports.db 'PRAGMA integrity_check'` debe responder `ok`.

**Sin Docker**: `sqlite3 data/tracereports.db '.backup respaldo.db'` y copia `data/screenshots/`; o
detén el servidor y copia la carpeta `data` completa (incluidos `-wal` y `-shm`).

## Siguiente paso

- [Configuración](configuration.md): seguridad, IA y notificaciones.
- [Python](python.md) · [Go](go.md) · [API REST](api.md)
