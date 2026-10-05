// Contexto de la ejecución: rama y commit. El servidor solo compara una ejecución con otras del
// mismo proyecto, ambiente y rama. Se leen de las variables de los CI más comunes o de git.

import { execFileSync } from "node:child_process";

const BRANCH_VARS = ["TRACEREPORTS_BRANCH", "GITHUB_HEAD_REF", "GITHUB_REF_NAME", "CI_COMMIT_REF_NAME", "BITBUCKET_BRANCH",
  "BUILD_SOURCEBRANCHNAME", "BRANCH_NAME", "CIRCLE_BRANCH", "GIT_BRANCH"];
const COMMIT_VARS = ["TRACEREPORTS_COMMIT", "GITHUB_SHA", "CI_COMMIT_SHA", "BITBUCKET_COMMIT", "BUILD_SOURCEVERSION", "CIRCLE_SHA1", "GIT_COMMIT"];

function git(...args) {
  try {
    return execFileSync("git", args, { encoding: "utf8", timeout: 2000, stdio: ["ignore", "pipe", "ignore"] }).trim();
  } catch {
    return "";
  }
}

function firstEnv(vars) {
  for (const v of vars) {
    const value = (process.env[v] || "").trim();
    if (value) return value.replace(/^origin\//, "");
  }
  return "";
}

export function detectBranch() {
  const branch = firstEnv(BRANCH_VARS) || git("rev-parse", "--abbrev-ref", "HEAD");
  return branch === "HEAD" ? "" : branch;
}

export function detectCommit() {
  return firstEnv(COMMIT_VARS) || git("rev-parse", "HEAD");
}
