import fs from "node:fs";
import YAML from "yaml";

export type RecordConf = {
  BaseDir?: string;
  MatchDirTemplate?: string;
  MatchNameTemplate?: string;
};

export type DanmuConf = {
  Enabled?: boolean;
  AppID?: string;
  AppKey?: string;
  VideoOffsetSeconds?: number;
};

export type Config = {
  PostgresConf?: {
    DSN?: string;
  };
  RecordConf?: RecordConf;
  DanmuConf?: DanmuConf;
};

export function loadConfig(path: string): Config {
  return YAML.parse(fs.readFileSync(path, "utf8")) as Config;
}

export function recordConfWithDefaults(conf?: RecordConf): Required<RecordConf> {
  return {
    BaseDir: conf?.BaseDir || "/records",
    MatchDirTemplate: conf?.MatchDirTemplate || "{{.Event}}/{{.Zone}}/{{.MatchName}}",
    MatchNameTemplate:
      conf?.MatchNameTemplate || "{{.Order}}. {{.RedSchool}}-{{.RedName}} VS {{.BlueSchool}}-{{.BlueName}}",
  };
}

export function danmuConfWithDefaults(conf?: DanmuConf): Required<DanmuConf> {
  return {
    Enabled: Boolean(conf?.Enabled),
    AppID: conf?.AppID || "",
    AppKey: conf?.AppKey || "",
    VideoOffsetSeconds: conf?.VideoOffsetSeconds ?? -3,
  };
}
