import fs from "node:fs";
import { setTimeout as sleep } from "node:timers/promises";
import path from "node:path";
import { Pool } from "pg";
import { danmuConfWithDefaults, loadConfig, recordConfWithDefaults } from "./config.js";
import { renderMatchDir, resolveUnderBase } from "./pathfmt.js";
import { DanmuStats, statsBucketSeconds } from "./stats.js";
import { BilibiliXMLWriter, type DanmuMessage } from "./xml.js";

const seenMessageLimit = 5000;
const historyLimit = 80;
const outputFileName = "主视角.danmuku.xml";

type RoundInfo = {
  id: number;
  roundNo: number;
  status: string;
  startedAt: Date;
  endedAt: Date | null;
  matchID: string;
  event: string;
  zone: string;
  order: number;
  redSchool: string;
  redName: string;
  blueSchool: string;
  blueName: string;
};

type LeanMessage = {
  id?: unknown;
  messageId?: unknown;
  timestamp?: unknown;
  content?: {
    _lctext?: unknown;
    text?: unknown;
    _lcattrs?: Record<string, unknown>;
    attributes?: Record<string, unknown>;
  };
  conversation?: { id?: unknown };
  conversationId?: unknown;
  cid?: unknown;
  getText?: () => string;
  getAttributes?: () => Record<string, unknown>;
};

const args = parseArgs(process.argv.slice(2));
const configFile = args.f || "etc/config.yml";
const roundID = Number(args.round || 0);
const chatRoomID = String(args["chat-room"] || process.env.DANMU_CHAT_ROOM_ID || "").trim();
if (!roundID) {
  fatal("round id is required");
}
if (!chatRoomID) {
  fatal("chat room id is required");
}

const config = loadConfig(configFile);
const recordConf = recordConfWithDefaults(config.RecordConf);
const danmuConf = danmuConfWithDefaults(config.DanmuConf);

if (!danmuConf.Enabled) {
  process.exit(0);
}
if (!danmuConf.AppID || !danmuConf.AppKey) {
  fatal("DanmuConf AppID/AppKey is required");
}

const pool = new Pool({ connectionString: config.PostgresConf.DSN });
let writer = null as BilibiliXMLWriter | null;
const stats = new DanmuStats();
let runtimeClient = null as { close?: () => Promise<void> } | null;
let room = null as { count?: () => Promise<number>; leave?: () => Promise<void> } | null;
let shuttingDown = false;

process.on("SIGTERM", () => {
  shuttingDown = true;
});
process.on("SIGINT", () => {
  shuttingDown = true;
});

try {
  await run();
} catch (error) {
  writer?.abort();
  console.error(error instanceof Error ? error.message : String(error));
  process.exitCode = 1;
} finally {
  try {
    if (room?.leave) {
      await room.leave();
    }
  } catch {
    // ignore leave failures
  }
  try {
    if (runtimeClient?.close) {
      await runtimeClient.close();
    }
  } catch {
    // ignore close failures
  }
  await pool.end();
}

async function run() {
  const round = await loadRound(roundID);
  const roundDir = outputRoundDir(round);
  const outputPath = path.join(roundDir, outputFileName);
  writer = new BilibiliXMLWriter(outputPath);
  await writer.open();

  const runtime = await connectLeanCloud(chatRoomID, round);
  runtimeClient = runtime.client;
  room = runtime.room;
  const stopSampling = startOnlineSampler(runtime.room, round);
  await waitUntilRoundEnded(round.id);
  stopSampling();
  const finalRound = await loadRound(round.id);
  await writer.close();
  await stats.writeOutputs(roundDir, {
    roundNo: finalRound.roundNo,
    startedAt: finalRound.startedAt,
    endedAt: finalRound.endedAt,
  });
}

async function connectLeanCloud(roomID: string, round: RoundInfo) {
  const { Realtime, Event } = await loadLeancloudRuntime();
  const realtime = new Realtime({
    appId: danmuConf.AppID,
    appKey: danmuConf.AppKey,
    server: {
      RTMRouter: "https://router-g0-push.leancloud.cn",
      api: "https://api.leancloud.cn",
    },
  });
  const clientID = `rm-monitor-danmu-${round.id}-${Date.now().toString(36)}`;
  const client = await realtime.createIMClient(clientID);
  const room = await resolveRoom(client, roomID);
  if (room?.join) {
    await room.join();
  }

  const seen = new Set<string>();
  const seenOrder: string[] = [];
  let hydrating = true;
  const pending: LeanMessage[] = [];
  const eventNames = new Set<string>(["message"]);
  if (typeof Event?.MESSAGE === "string" && Event.MESSAGE.trim()) {
    eventNames.add(Event.MESSAGE);
  }
  const handler = (message: LeanMessage) => {
    if (hydrating) {
      pending.push(message);
      return;
    }
    writeMessageIfValid(message, round, seen, seenOrder);
  };
  for (const eventName of eventNames) {
    room.on(eventName, handler);
  }

  await writeHistory(room, round, seen, seenOrder);
  hydrating = false;
  pending.sort((a, b) => messageTimestamp(a) - messageTimestamp(b));
  for (const message of pending) {
    writeMessageIfValid(message, round, seen, seenOrder);
  }
  return { client, room };
}

async function writeHistory(
  room: { queryMessages?: (opts: unknown) => Promise<LeanMessage[]> },
  round: RoundInfo,
  seen: Set<string>,
  seenOrder: string[],
) {
  if (!room.queryMessages || historyLimit <= 0) {
    return;
  }
  const history = await room.queryMessages({ limit: historyLimit });
  if (!Array.isArray(history)) {
    return;
  }
  history.sort((a, b) => messageTimestamp(a) - messageTimestamp(b));
  for (const message of history) {
    writeMessageIfValid(message, round, seen, seenOrder);
  }
}

function writeMessageIfValid(message: LeanMessage, round: RoundInfo, seen: Set<string>, seenOrder: string[]) {
  const id = stableMessageID(message);
  if (seen.has(id)) {
    return;
  }
  const timestamp = messageTimestamp(message);
  if (!Number.isFinite(timestamp) || timestamp < round.startedAt.getTime()) {
    return;
  }
  if (round.endedAt && timestamp > round.endedAt.getTime() + 5000) {
    return;
  }
  const text = messageText(message);
  if (!text) {
    return;
  }
  rememberSeen(id, seen, seenOrder);
  const attrs = messageAttributes(message);
  const item: DanmuMessage = {
    id,
    offsetSeconds: (timestamp - round.startedAt.getTime()) / 1000,
    timestamp,
    text,
    username: String(attrs.username ?? ""),
    nickname: String(attrs.nickname ?? ""),
    mode: attrs.mode,
    color: attrs.color,
  };
  stats.recordDanmu(item.offsetSeconds);
  writer?.writeMessage(item);
}

function startOnlineSampler(room: { count?: () => Promise<number> }, round: RoundInfo): () => void {
  let stopped = false;
  const sample = async () => {
    const offsetSeconds = (Date.now() - round.startedAt.getTime()) / 1000;
    try {
      if (typeof room.count !== "function") {
        stats.recordOnline(offsetSeconds, null);
      } else {
        const count = await room.count();
        stats.recordOnline(offsetSeconds, Number.isFinite(count) ? Number(count) : null);
      }
    } catch {
      stats.recordOnline(offsetSeconds, null);
    }
  };
  void sample();
  const timer = setInterval(() => {
    if (!stopped) {
      void sample();
    }
  }, statsBucketSeconds * 1000);
  return () => {
    stopped = true;
    clearInterval(timer);
  };
}

function rememberSeen(id: string, seen: Set<string>, seenOrder: string[]) {
  seen.add(id);
  seenOrder.push(id);
  while (seenOrder.length > seenMessageLimit) {
    const old = seenOrder.shift();
    if (old) {
      seen.delete(old);
    }
  }
}

async function resolveRoom(client: any, roomID: string) {
  try {
    const room = await client.getChatRoomQuery().equalTo("objectId", roomID).compact(true).limit(1).first();
    if (room) {
      return room;
    }
  } catch {
    // fallback below
  }
  return client.getConversation(roomID, true);
}

async function loadRound(id: number): Promise<RoundInfo> {
  const result = await pool.query(
    `
      SELECT
        mr.id,
        mr.round_no,
        mr.status,
        mr.started_at,
        mr.ended_at,
        m.id AS match_id,
        m.event,
        m.zone,
        m."order",
        red.school_name AS red_school,
        red.name AS red_name,
        blue.school_name AS blue_school,
        blue.name AS blue_name
      FROM match_rounds mr
      JOIN matches m ON mr.match_rounds = m.id
      JOIN teams red ON m.team_red_matches = red.id
      JOIN teams blue ON m.team_blue_matches = blue.id
      WHERE mr.id = $1
    `,
    [id],
  );
  if (result.rowCount !== 1) {
    throw new Error(`match round ${id} not found`);
  }
  const row = result.rows[0];
  return {
    id: Number(row.id),
    roundNo: Number(row.round_no),
    status: String(row.status),
    startedAt: new Date(row.started_at),
    endedAt: row.ended_at ? new Date(row.ended_at) : null,
    matchID: String(row.match_id),
    event: String(row.event),
    zone: String(row.zone),
    order: Number(row.order),
    redSchool: String(row.red_school ?? ""),
    redName: String(row.red_name ?? ""),
    blueSchool: String(row.blue_school ?? ""),
    blueName: String(row.blue_name ?? ""),
  };
}

async function waitUntilRoundEnded(id: number) {
  for (;;) {
    if (shuttingDown) {
      throw new Error("shutting down");
    }
    const result = await pool.query(`SELECT status FROM match_rounds WHERE id = $1`, [id]);
    if (result.rowCount !== 1) {
      throw new Error(`match round ${id} not found during watch`);
    }
    if (String(result.rows[0].status) === "ENDED") {
      return;
    }
    await sleep(2000);
  }
}

function outputRoundDir(round: RoundInfo): string {
  const rel = renderMatchDir(recordConf, {
    Event: round.event,
    Zone: round.zone,
    Order: round.order,
    RedSchool: round.redSchool,
    RedName: round.redName,
    BlueSchool: round.blueSchool,
    BlueName: round.blueName,
    RoundNo: round.roundNo,
    Role: "",
  });
  return resolveUnderBase(recordConf.BaseDir, path.posix.join(rel, `Round-${round.roundNo}`));
}

async function loadLeancloudRuntime(): Promise<any> {
  const scope = globalThis as any;
  if (!("window" in scope)) {
    scope.window = scope;
  }
  if (!("localStorage" in scope)) {
    const entries = new Map<string, string>();
    scope.localStorage = {
      get length() {
        return entries.size;
      },
      clear: () => entries.clear(),
      getItem: (key: string) => entries.get(String(key)) ?? null,
      key: (index: number) => Array.from(entries.keys())[index] ?? null,
      removeItem: (key: string) => entries.delete(String(key)),
      setItem: (key: string, value: string) => entries.set(String(key), String(value)),
    };
  }
  return import("leancloud-realtime");
}

function messageText(message: LeanMessage): string {
  const text =
    typeof message.getText === "function"
      ? message.getText()
      : String(message.content?._lctext ?? message.content?.text ?? "").trim();
  return String(text ?? "").trim();
}

function messageAttributes(message: LeanMessage): Record<string, unknown> {
  return message.getAttributes?.() || message.content?._lcattrs || message.content?.attributes || {};
}

function messageTimestamp(message: LeanMessage): number {
  const value = message.timestamp;
  if (typeof value === "number" && Number.isFinite(value)) {
    return Math.abs(value) < 1_000_000_000_000 ? value * 1000 : value;
  }
  if (value instanceof Date) {
    return value.getTime();
  }
  if (typeof value === "string" && value.trim()) {
    const numeric = Number(value);
    if (Number.isFinite(numeric)) {
      return Math.abs(numeric) < 1_000_000_000_000 ? numeric * 1000 : numeric;
    }
    const parsed = Date.parse(value);
    if (Number.isFinite(parsed)) {
      return parsed;
    }
  }
  return Date.now();
}

function stableMessageID(message: LeanMessage): string {
  const raw = message.id || message.messageId || `${messageTimestamp(message)}:${messageText(message)}`;
  return String(raw);
}

function parseArgs(values: string[]): Record<string, string> {
  const out: Record<string, string> = {};
  for (let i = 0; i < values.length; i++) {
    const key = values[i];
    if (!key.startsWith("-")) {
      continue;
    }
    const clean = key.replace(/^-+/, "");
    out[clean] = values[i + 1] ?? "";
    i++;
  }
  return out;
}

function fatal(message: string): never {
  console.error(message);
  process.exit(1);
}
