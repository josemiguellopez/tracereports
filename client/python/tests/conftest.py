import os

# Los tests corren dentro del repositorio: sin esto, el cliente leería el .env real del proyecto
# (token y URL del servidor de desarrollo). Los tests del .env lo activan a propósito.
os.environ["TRACEREPORTS_ENV_FILE"] = "off"
