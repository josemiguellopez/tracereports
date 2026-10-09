# Reporte local en tu PC

🌐 [English](../en/local-report.md) · **Español**

Cuando ejecutas tus pruebas desde tu PC, TraceReports puede dejarte **dos reportes de la misma
ejecución**:

- **en el servidor**, el de siempre, para que lo vea todo el equipo;
- **en tu PC**, un `index.html` de esa ejecución que se abre con doble clic, sin servidor ni
  internet, con los pasos, capturas, red, replay, el resumen para **Escalar** y la decisión de
  **Release**.

El reporte local también aparece si **olvidaste el token**, si el servidor está **caído** o si se
cayó **a mitad** de la suite: la evidencia de tu ejecución nunca se pierde.

> Esta guía es para quien ejecuta las pruebas. El detalle técnico (formato de la grabación, shards,
> `push --force`) está en [Sin servidor: grabar, reportar y subir después](offline.md).

## 1. Configuración en 2 minutos

Agrega estas líneas al `.env` de tu proyecto de pruebas (el mismo donde están `TRACEREPORTS_URL` y
`TRACEREPORTS_TOKEN`). Los clientes de Python, JavaScript, Java y Go lo leen solos:

```dotenv
# envía al servidor y además guarda una copia local de cada ejecución
TRACEREPORTS_OFFLINE=both
# carpeta donde cada ejecución crea la suya (relativa a donde lanzas los tests)
TRACEREPORTS_OFFLINE_BASE=output/tracereports
```

| Variable | Qué hace |
| --- | --- |
| `TRACEREPORTS_OFFLINE=both` | Envía al servidor **y** guarda la copia local. Con el default (`auto`) solo se graba en local si el servidor no responde o rechaza el token |
| `TRACEREPORTS_OFFLINE_BASE` | Dónde se crean las carpetas de cada ejecución. La carpeta se crea sola si no existe |
| `TRACEREPORTS_BIN` | Opcional: ruta del binario `tracereports` (ver el punto 2) |
| `TRACEREPORTS_OFFLINE_KEEP=1` | Opcional: conservar también la grabación cruda aunque todo haya llegado bien |

No uses `TRACEREPORTS_OFFLINE_DIR` en un `.env` de uso diario: es una carpeta **fija** pensada para
que los procesos de una misma ejecución (workers, shards) la compartan, y todas tus ejecuciones
terminarían escribiendo en el mismo lugar.

## 2. El binario que arma el HTML

El HTML lo arma el binario `tracereports` con el **mismo código del servidor** (enmascarado de
contraseñas y tokens, incidentes, timeline…), por eso queda igual al que exporta el servidor. Los
clientes lo buscan en este orden:

1. `TRACEREPORTS_BIN` del `.env` o del entorno;
2. el `PATH`;
3. la caché de tu usuario;
4. **descarga automática** desde GitHub Releases, una sola vez, verificando su huella SHA-256.

Con la descarga automática no tienes que hacer nada. Si prefieres instalarlo tú (o no tienes
internet), descárgalo de [Releases](https://github.com/josemiguellopez/tracereports/releases):

| Sistema | Archivo | Pasos |
| --- | --- | --- |
| Windows | `tracereports_<versión>_windows_amd64.zip` | Descomprime y apunta `TRACEREPORTS_BIN=C:/ruta/tracereports.exe` |
| macOS Apple Silicon (M1, M2, M3…) | `..._darwin_arm64.tar.gz` | Ver abajo |
| macOS Intel | `..._darwin_amd64.tar.gz` | Ver abajo |
| Linux | `..._linux_amd64.tar.gz` o `_arm64` | `chmod +x tracereports` y déjalo en el `PATH` |

En macOS:

```bash
tar -xzf tracereports_*_darwin_arm64.tar.gz
chmod +x tracereports
xattr -d com.apple.quarantine tracereports   # macOS bloquea binarios descargados sin firmar
sudo mv tracereports /usr/local/bin/          # o usa TRACEREPORTS_BIN con la ruta donde lo dejes
```

Si tienes Go y el repositorio, también puedes compilarlo: `go build -o tracereports.exe ./cmd`
(en macOS o Linux, `-o tracereports`).

La caché está en `%LOCALAPPDATA%\tracereports\` (Windows), `~/Library/Caches/tracereports/`
(macOS) o `~/.cache/tracereports/` (Linux). Para no descargar nunca: `TRACEREPORTS_BIN_DOWNLOAD=0`.

### 2.1 Paso a paso: usar tu propio `tracereports.exe`, sin descargar de GitHub

Útil si en tu red no hay acceso a GitHub, si la versión todavía no está publicada o si tu equipo
reparte el binario por su cuenta.

1. **Consigue el binario**, una de estas formas:
   - compílalo desde el repositorio (necesitas Go):

     ```powershell
     cd C:\dev\tracereports
     go build -o tracereports.exe ./cmd
     ```

   - o copia el `tracereports.exe` que te pase tu equipo, o el de [Releases](https://github.com/josemiguellopez/tracereports/releases).
2. **Déjalo en una ruta fija**, por ejemplo `C:\tools\tracereports\tracereports.exe` (en macOS o
   Linux, `/usr/local/bin/tracereports` o tu carpeta de herramientas).
3. **Comprueba que funciona**: `C:\tools\tracereports\tracereports.exe report --help` debe mostrar
   la ayuda.
4. **Apúntalo en tu `.env`** y desactiva la descarga:

   ```dotenv
   TRACEREPORTS_OFFLINE=both
   TRACEREPORTS_OFFLINE_BASE=output/tracereports
   TRACEREPORTS_BIN=C:/tools/tracereports/tracereports.exe
   TRACEREPORTS_BIN_DOWNLOAD=0
   ```

   Usa `/` en la ruta, como en el ejemplo.
5. **Ejecuta tus tests** como siempre. Al terminar, la consola muestra la ruta del
   `report/index.html` de esa ejecución.
6. **Para armar a mano** una carpeta que quedó sin HTML:

   ```powershell
   C:\tools\tracereports\tracereports.exe report output\tracereports\<carpeta> -o output\tracereports\<carpeta>\report
   ```

Cuando actualices TraceReports, reemplaza también el binario por el de la misma versión.

## 3. Qué queda en tu carpeta

Cada ejecución crea su propia carpeta con el **nombre de la ejecución**, la fecha-hora y un id:

```
output/tracereports/
├── orangehrm-pim-20261009-100431-02553c/
│   └── report/
│       └── index.html        ← ábrelo con doble clic
└── orangehrm-pim-20261009-101210-7f3a91/
    └── report/index.html
```

Así puedes lanzar varias ejecuciones **en paralelo** sin que se pisen, y reconocer cuál es cuál.

| Al terminar | Queda en la carpeta |
| --- | --- |
| Todo llegó al servidor y se armó el HTML | Solo `report/` |
| Algo no llegó al servidor (token olvidado, servidor caído) | `report/` **y** la grabación cruda, para subirla después |
| No se pudo armar el HTML (sin binario y sin internet) | La grabación cruda y un aviso con el comando para armarlo |
| `TRACEREPORTS_OFFLINE_KEEP=1` | Siempre `report/` y la grabación cruda |

**La grabación cruda (`events-*.jsonl`, `bodies/`) tiene los datos tal como los capturó el test,
sin enmascarar.** El `index.html` sí sale enmascarado. No compartas la grabación cruda y agrega la
carpeta a tu `.gitignore`.

Al terminar, la consola (el resumen de pytest, el reporter de Playwright o el log en Java y Go)
muestra las dos ubicaciones: la URL del reporte en el servidor y la ruta del `index.html` local.

## 4. Qué trae el reporte local

| Incluye | No incluye (necesitan el servidor) |
| --- | --- |
| Pasos, capturas, timeline y replay | **Métricas** (cruzan muchas ejecuciones) |
| Red con *Copiar como cURL* y mocks | Enviar a **Teams o Slack** y crear **tickets** |
| Diagnóstico y locators sugeridos | Generar o re-analizar con **IA** (se ve el diagnóstico que ya existía) |
| **Escalar**: el resumen de la ejecución y de cada test fallido, para Negocio, QA y Desarrollo, en español e inglés, listo para copiar como texto, Markdown, correo o imagen | **Ajustes** del servidor |
| **Release**: la decisión de esa ejecución con sus criterios | Comparar con ejecuciones anteriores |

## 5. Casos comunes

- **Olvidé el token.** El servidor rechaza la ejecución, pero el `index.html` local se arma igual.
  Cuando tengas el token: `tracereports push <carpeta>` y la ejecución llega al servidor.
- **El servidor se cayó a mitad de la suite.** Los tests siguen; la copia local queda completa,
  con su HTML y la grabación cruda. Súbela después con `push`: se crea una ejecución completa,
  separada de la parcial que alcanzó a llegar.
- **Varias ejecuciones a la vez.** Cada una tiene su carpeta (punto 3).
- **Sin internet.** Instala el binario a mano (punto 2) y usa `TRACEREPORTS_BIN_DOWNLOAD=0`.

## 6. Subir después al servidor

```bash
tracereports push output/tracereports/orangehrm-pim-20261009-100431-02553c --token <token>
```

Subir dos veces la misma carpeta no duplica nada. Si la copia ya había llegado completa, `push` se
niega para no duplicarla. Si solo queda `report/`, no hay nada que subir: ya estaba en el servidor.

## 7. Si no aparece el `index.html`

| Lo que ves | Causa | Solución |
| --- | --- | --- |
| Aviso *local report unavailable (…404…)* | La versión del binario aún no está publicada en GitHub | Usa un binario local (punto 2.1) |
| Aviso de descarga desactivada o sin red | `TRACEREPORTS_BIN_DOWNLOAD=0` o sin internet | Igual que arriba |
| La carpeta tiene `events-*.jsonl` pero no `report/` | No se encontró el binario | Ármalo a mano: `tracereports report <carpeta> -o <carpeta>/report` |
| No se creó ninguna carpeta | `TRACEREPORTS_OFFLINE` no es `both` y el servidor respondió bien | Revisa el `.env` (punto 1) |
| Una ejecución esperó ~60 s al terminar | Otra ejecución estaba descargando el binario, o quedó un bloqueo de una descarga interrumpida | Se resuelve solo: un bloqueo de más de 5 minutos se retoma |

Los avisos siempre explican qué pasó y el comando para resolverlo. Los tests nunca fallan por el
reporte local.

## 8. Ejemplo completo con OrangeHRM

El ejemplo [`examples/orangehrm`](../../examples/orangehrm) ya guarda su copia local en
`examples/orangehrm/output/tracereports/`, venga de donde venga el comando.

1. En el `.env` de la raíz del repositorio:

   ```dotenv
   TRACEREPORTS_URL=http://localhost:8080
   TRACEREPORTS_TOKEN=<tu token>
   TRACEREPORTS_OFFLINE=both
   ```

2. Ejecuta: `python examples/orangehrm/tests/test_orangehrm_pim.py`.
3. Al terminar, la consola muestra la URL del servidor y la ruta del reporte local. Abre
   `examples/orangehrm/output/tracereports/orangehrm-pim-<fecha>-<id>/report/index.html`.
4. Prueba el caso del token olvidado: cambia `TRACEREPORTS_TOKEN` por uno incorrecto y ejecuta de
   nuevo. El servidor rechaza la ejecución, pero el reporte local aparece igual.
