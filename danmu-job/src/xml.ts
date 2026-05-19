import fs from "node:fs";
import { open, type FileHandle } from "node:fs/promises";
import path from "node:path";
import { createHash } from "node:crypto";

export type DanmuMessage = {
  id: string;
  offsetSeconds: number;
  timestamp: number;
  text: string;
  username?: string;
  nickname?: string;
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
    this.write(`  <d p="${seconds},${mode},25,${color},${unix},0,${userHash},${rowID}">${escapeXML(message.text)}</d>\n`);
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
