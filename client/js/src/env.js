// Variables de entorno del cliente: TRACEREPORTS_<NOMBRE>.
//
// Si la variable no está en el entorno, se busca en el archivo .env del proyecto (el mismo que lee
// el servidor): el token vive en un solo lugar, ignorado por git, sin definirlo en cada terminal ni
// de forma global. Las variables del entorno siempre ganan (en CI llegan como secrets). Del archivo
// solo se usan las claves TRACEREPORTS_*.
//
// El .env se busca desde la carpeta actual hacia arriba, hasta la raíz del repositorio (la carpeta
// con .git). TRACEREPORTS_ENV_FILE indica otro archivo, u "off" para no leer ninguno.

import { existsSync, readFileSync, statSync } from "node:fs";
import path from "node:path";

const PREFIX = "TRACEREPORTS_";

let fileValues = null;
let filePath = null;

/** Líneas KEY=VALUE con las mismas reglas que el servidor. */
export function parseEnvFile(file) {
  const values = {};
  let text;
  try {
    text = readFileSync(file, "utf8").replace(/^﻿/, "");
  } catch {
    return values;
  }
  for (const raw of text.split(/\r?\n/)) {
    let line = raw.trim();
    if (!line || line.startsWith("#")) continue;
    if (line.startsWith("export ")) line = line.slice("export ".length);
    const eq = line.indexOf("=");
    if (eq < 0) continue;
    const key = line.slice(0, eq).trim();
    if (!key || /[ \t]/.test(key)) continue;
    let val = line.slice(eq + 1).trim();
    if (val.length >= 2 && val[0] === val[val.length - 1] && (val[0] === '"' || val[0] === "'")) {
      val = val.slice(1, -1);
    } else if (val.includes(" #")) {
      val = val.slice(0, val.indexOf(" #")).trim();
    }
    values[key] = val;
  }
  return values;
}

function isFile(p) {
  try {
    return statSync(p).isFile();
  } catch {
    return false;
  }
}

/** El .env que corresponde, o null si no hay o está desactivado. */
export function findEnvFile(start = process.cwd()) {
  const explicit = process.env[`${PREFIX}ENV_FILE`];
  if (explicit) return explicit.trim().toLowerCase() === "off" ? null : explicit;
  let dir = path.resolve(start);
  for (;;) {
    const candidate = path.join(dir, ".env");
    if (isFile(candidate)) return candidate;
    const parent = path.dirname(dir);
    if (existsSync(path.join(dir, ".git")) || parent === dir) return null;
    dir = parent;
  }
}

function fromFile() {
  if (fileValues === null) {
    filePath = findEnvFile();
    const all = filePath ? parseEnvFile(filePath) : {};
    fileValues = Object.fromEntries(Object.entries(all).filter(([k]) => k.startsWith(PREFIX)));
  }
  return fileValues;
}

/** Ruta del .env del que se leyó alguna variable (para los mensajes), o null. */
export function envFileInUse() {
  return Object.keys(fromFile()).length ? filePath : null;
}

/** Vuelve a buscar el .env en la próxima lectura (tests). */
export function resetEnvFile() {
  fileValues = null;
  filePath = null;
}

/** TRACEREPORTS_<name>: primero del entorno, después del .env del proyecto. */
export function env(name, fallback = undefined) {
  for (const v of [process.env[PREFIX + name], fromFile()[PREFIX + name]]) {
    if (v !== undefined && v !== "") return v;
  }
  return fallback;
}
