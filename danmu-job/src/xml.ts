import fs from "node:fs";
import { open, type FileHandle } from "node:fs/promises";
import path from "node:path";
import { createHash } from "node:crypto";
import { create } from "xmlbuilder2";

export type DanmuMessage = {
  id: string;
  offsetSeconds: number;
  timestamp: number;
  text: string;
  username?: string;
  nickname?: string;
  attributes?: Record<string, unknown>;
  mode?: unknown;
  color?: unknown;
};

export class BilibiliXMLWriter {
  private handle: FileHandle | null = null;
  private readonly tmpPath: string;
  private tail: Promise<void> = Promise.resolve();

  constructor(private readonly finalPath: string) {
    this.tmpPath = `${finalPath}.tmp`;
  }

  async open() {
    fs.mkdirSync(path.dirname(this.finalPath), { recursive: true });
    this.handle = await open(this.tmpPath, "w", 0o644);
    this.write(`<?xml version="1.0" encoding="UTF-8"?>\n<i>\n`);
    this.write(`  <chatserver>rm-monitor</chatserver>\n`);
    this.write(`  <chatid>1</chatid>\n`);
    this.write(`  <mission>0</mission>\n`);
    this.write(`  <maxlimit>1000</maxlimit>\n`);
    this.write(`  <state>0</state>\n`);
    this.write(`  <real_name>0</real_name>\n`);
    this.write(`  <source>rm-monitor</source>\n`);
  }

  writeMessage(message: DanmuMessage) {
    const seconds = Math.max(0, message.offsetSeconds).toFixed(3);
    const mode = toBilibiliMode(message.mode);
    const color = toBilibiliColor(message.color);
    const unix = Math.max(0, Math.floor(message.timestamp / 1000));
    const userHash = stableID(message.username || message.nickname || "anonymous").slice(0, 12);
    const rowID = stableID(message.id).slice(0, 16);
    const attrs = {
      p: `${seconds},${mode},25,${color},${unix},0,${userHash},${rowID}`,
      ...renderCustomAttributes(message.attributes),
    };
    const node = create({
      d: {
        ...Object.fromEntries(Object.entries(attrs).map(([key, value]) => [`@${key}`, value])),
        "#": message.text,
      },
    }).end({ headless: true });
    this.write(`  ${node}\n`);
  }

  async close() {
    if (this.handle === null) {
      return;
    }
    this.write(`</i>\n`);
    await this.tail;
    await this.handle.sync();
    await this.handle.close();
    this.handle = null;
    fs.renameSync(this.tmpPath, this.finalPath);
  }

  abort() {
    if (this.handle !== null) {
      void this.handle.close().catch(() => undefined);
      this.handle = null;
    }
    try {
      fs.unlinkSync(this.tmpPath);
    } catch {
      // ignore cleanup failures
    }
  }

  private write(text: string) {
    if (this.handle === null) {
      throw new Error("XML writer is not open");
    }
    const handle = this.handle;
    const next = this.tail.then(async () => {
      await handle.writeFile(text);
    });
    next.catch(() => undefined);
    this.tail = next;
  }
}

export function escapeXML(value: string): string {
  return value.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;").replace(/'/g, "&apos;");
}

export function renderCustomAttributes(attributes: Record<string, unknown> | undefined): Record<string, string> {
  if (!attributes || Object.keys(attributes).length === 0) {
    return {};
  }
  const rendered: Record<string, string> = {};
  const used = new Set<string>();
  for (const [key, value] of Object.entries(attributes)) {
    const attrName = uniqueAttributeName(`lc_${sanitizeAttributeName(key)}`, used);
    rendered[attrName] = attributeValue(value);
  }
  return rendered;
}

export function toBilibiliMode(value: unknown): number {
  const n = typeof value === "number" ? value : typeof value === "string" && value.trim() ? Number(value) : 0;
  if (n === 1) {
    return 5;
  }
  if (n === 2) {
    return 4;
  }
  return 1;
}

export function toBilibiliColor(value: unknown): number {
  if (typeof value !== "string") {
    return 16777215;
  }
  const text = value.trim();
  const hex = /^#?([0-9a-fA-F]{6})$/.exec(text);
  if (!hex) {
    return 16777215;
  }
  return parseInt(hex[1], 16);
}

function stableID(value: string): string {
  return createHash("sha1").update(value).digest("hex");
}

function sanitizeAttributeName(value: string): string {
  const text = value.trim().replace(/[^A-Za-z0-9_.-]+/g, "_").replace(/^[^A-Za-z_]+/, "");
  return text || "attr";
}

function uniqueAttributeName(base: string, used: Set<string>): string {
  let name = base;
  let index = 2;
  while (used.has(name)) {
    name = `${base}_${index}`;
    index += 1;
  }
  used.add(name);
  return name;
}

function attributeValue(value: unknown): string {
  if (value === undefined || value === null) {
    return "";
  }
  if (typeof value === "string") {
    return value;
  }
  if (typeof value === "number" || typeof value === "boolean" || typeof value === "bigint") {
    return String(value);
  }
  return JSON.stringify(toJSONSafe(value));
}

function toJSONSafe(value: unknown): unknown {
  if (value === undefined) {
    return null;
  }
  if (value === null || typeof value === "string" || typeof value === "number" || typeof value === "boolean") {
    return value;
  }
  if (typeof value === "bigint") {
    return value.toString();
  }
  if (Array.isArray(value)) {
    return value.map(toJSONSafe);
  }
  if (typeof value === "object") {
    const out: Record<string, unknown> = {};
    for (const [key, item] of Object.entries(value as Record<string, unknown>)) {
      out[key] = toJSONSafe(item);
    }
    return out;
  }
  return String(value);
}
