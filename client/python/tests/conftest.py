import os

# Los tests corren dentro del repositorio: sin esto, el cliente leería el .env real del proyecto
# (token y URL del servidor de desarrollo). Los tests del .env lo activan a propósito.
os.environ["TRACEREPORTS_ENV_FILE"] = "off"
# los tests nunca descargan el binario de GitHub (los de descarga usan su servidor local)
os.environ.setdefault("TRACEREPORTS_BIN_DOWNLOAD", "0")
