import path from "node:path";
import type { RecordConf } from "./config.js";

export type PathData = {
  Event: string;
  Zone: string;
  MatchName?: string;
  Order: number;
  RedSchool: string;
  RedName: string;
  BlueSchool: string;
  BlueName: string;
  RoundNo: number;
  Role: string;
};

export function renderMatchDir(conf: Required<RecordConf>, data: PathData): string {
  const withName = {
    ...data,
    MatchName: executeTemplate(conf.MatchNameTemplate, data),
  };
  return sanitizePath(executeTemplate(conf.MatchDirTemplate, withName));
}

export function resolveUnderBase(base: string, rel: string): string {
  if (!rel) {
    return path.normalize(base);
  }
  if (path.isAbsolute(rel) || rel.startsWith("/")) {
    return path.normalize(rel);
  }
  return path.join(base, rel.split("/").join(path.sep));
}

function executeTemplate(template: string, data: PathData): string {
  return template.replace(/\{\{\s*\.([A-Za-z0-9_]+)\s*\}\}/g, (_, key: keyof PathData) => {
    const value = data[key];
    return value === undefined || value === null ? "" : String(value);
  });
}

export function sanitizePath(input: string): string {
  const parts = input
    .split(/[\\/]/)
    .map((part) => {
      let out = part.trim().replace(/[<>:"\\|?*\x00-\x1f]/g, "_");
      out = out.replace(/^[. ]+|[. ]+$/g, "");
      return out || "_";
    })
    .filter((part) => part.length > 0);
  return parts.join("/");
}
