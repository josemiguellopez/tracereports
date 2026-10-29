# Política de estabilidad de la API

[English](../en/api-stability.md)

Esta política dice en qué puedes confiar al integrarte con TraceReports: la API REST (`/api/v1`), la CLI y
los clientes de tests. Rige desde esta versión; [1.0](release-1.0.md) es cuando su parte estable queda
congelada.

## Niveles

| Nivel | Qué | Promesa |
| --- | --- | --- |
| **Estable** | Los endpoints documentados en [API → Escritura](api.md#escritura-lo-que-usan-los-clientes-de-tests) y [Lectura](api.md#lectura-lo-que-usa-la-ui), las importaciones (JUnit XML, Allure), `GET /healthz`, los comandos `push`, `report`, `pr-comment`, `health`, `version`, y las funciones públicas de los cuatro clientes | Sin cambios incompatibles dentro de `/api/v1` desde 1.0; antes, solo con el proceso de abajo |
| **Experimental** | Los endpoints marcados como *experimentales* en la documentación: [acciones desde la interfaz](api.md#acciones-desde-la-interfaz-ia-y-escalamiento), FixLab (`/network/{id}/lab`, cápsulas), incidentes similares, la revisión de seguridad, Ajustes, las herramientas MCP | Pueden cambiar en cualquier versión menor, siempre escrito en el CHANGELOG |
| **Interno** | Todo lo no documentado (lo que la UI incluida llama para sí misma) | Sin promesa |

## Qué es un cambio incompatible

Incompatible: quitar o renombrar un endpoint, un campo o una opción de la CLI; cambiar el tipo o el
significado de un campo; volver obligatorio un parámetro opcional; cambiar un código de estado del que
depende un cliente; cambiar el valor por defecto de una configuración de modo que una instalación
existente se comporte distinto.

No es incompatible (puede llegar en cualquier versión menor): endpoints nuevos, parámetros opcionales
nuevos, **campos nuevos en las respuestas** (los clientes deben ignorar los campos que no conocen),
valores nuevos en un campo documentado como abierto (por ejemplo el proveedor de IA), mejores mensajes de
error.

## Antes de 1.0 (ahora)

TraceReports es pre-1.0: una versión menor (`0.x`) **puede** romper, pero solo si:

1. el CHANGELOG tiene una entrada **⚠️ Breaking** con su guía de migración;
2. cuando se puede, el comportamiento anterior se mantiene una versión menor más, o un interruptor
   explícito lo restaura (como `TRACEREPORTS_INSECURE=1` en el cambio de exposición en la red);
3. los arreglos de seguridad son la excepción: pueden romper de inmediato si mantener lo anterior deja el
   hueco abierto. El CHANGELOG lo dice.

## Desde 1.0

- [Versionado semántico](https://semver.org/lang/es/): cambios incompatibles en el nivel estable solo en una
  versión mayor nueva, que trae una ruta nueva (`/api/v2`) servida **en paralelo** con `/api/v1` por al
  menos **6 meses**.
- **Deprecación**: un endpoint, campo u opción estable se anuncia como deprecado al menos **dos versiones
  menores y 6 meses** antes de quitarlo. Mientras tanto sus respuestas llevan el header `Deprecation` y,
  cuando hay fecha, `Sunset` ([RFC 8594](https://www.rfc-editor.org/rfc/rfc8594)), y el CHANGELOG lo
  lista en *Deprecated*.
- **Clientes y servidor**: un cliente funciona con cualquier servidor de la misma versión mayor. Puede
  necesitar un servidor más nuevo solo para funciones nuevas, y se degrada sin romperse con uno anterior.
- **Datos**: la base se migra hacia adelante sola al arrancar; volver a una versión anterior no está
  soportado (respalda `DATA_DIR` antes de actualizar).

## Secciones de seguridad

Cada versión del CHANGELOG tiene una sección **Security**, aunque solo diga "No security changes". Lista
las vulnerabilidades corregidas (con su aviso cuando existe), los controles de seguridad nuevos y los
cambios de valores por defecto que afectan la exposición.
