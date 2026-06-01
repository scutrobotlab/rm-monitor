import fs from "node:fs";
import { setTimeout as sleep } from "node:timers/promises";
import path from "node:path";
import { danmuConfWithDefaults, loadConfig } from "./config.js";
import { DanmuStats, statsBucketSeconds } from "./stats.js";
import { BilibiliXMLWriter, type DanmuMessage } from "./xml.js";

const seenMessageLimit = 5000;
const historyLimit = 80;
const leancloudConnectTimeoutMs = 10000;
const leancloudHistoryTimeoutMs = 10000;
const leancloudOnlineCountTimeoutMs = 5000;
const outputFileName = "主视角.raw.danmuku.xml";
const recordMetaFileName = "record-meta.json";

type RoundInfo = {
  id: number;
  roundNo: number;
  startedAt: Date;
  endedAt: Date | null;
};

type DanmuJobContext = {
  schema?: string;
  match_round_id: number;
  chat_room_id: string;
  round_dir: string;
  started_at: string;
};

type RecordMeta = {
  schema: string;
  record_task_id: number;
  role: string;
  source_url: string;
  output_path: string;
  record_wall_started_at: string;
  record_wall_completed_at?: string | null;
  media_time_zero_wall_at: string;
  file_size: number;
  checksum: string;
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

type LeanRuntime = {
  client: { close?: () => Promise<void> };
  room: { count?: () => Promise<number>; leave?: () => Promise<void> };
};

const args = parseArgs(process.argv.slice(2));
const configFile = args.f || "etc/config.yml";
const jobContext = loadJobContext();
const roundID = Number(jobContext.match_round_id || 0);
const chatRoomID = String(jobContext.chat_room_id || "").trim();

const config = loadConfig(configFile);
const danmuConf = danmuConfWithDefaults(config.DanmuConf);

if (!danmuConf.Enabled) {
  process.exit(0);
}
if (!danmuConf.AppID || !danmuConf.AppKey) {
  fatal("DanmuConf AppID/AppKey is required");
}

let writer = null as BilibiliXMLWriter | null;
const stats = new DanmuStats();
let runtimeClient = null as { close?: () => Promise<void> } | null;
let room = null as { count?: () => Promise<number>; leave?: () => Promise<void> } | null;
let shuttingDown = false;
let mediaTimeZeroWallMs = 0;
let leancloudRealtime: any | null = null;
const diagnostics = {
  connectAttempts: 0,
  connectedAt: null as string | null,
  lastConnectError: "",
  acceptedMessages: 0,
  onlineSampleAttempts: 0,
  onlineSampleSuccess: 0,
  onlineSampleFailures: 0,
  onlineSampleTimeouts: 0,
};

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
  await writeJobError(error);
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
}

async function run() {
  if (!roundID) {
    throw new Error("match_round_id is required in job context");
  }
  if (!chatRoomID) {
    throw new Error("chat_room_id is required in job context");
  }
  const startedAt = new Date(jobContext.started_at);
  if (!Number.isFinite(startedAt.getTime())) {
    throw new Error(`invalid started_at in job context: ${jobContext.started_at}`);
  }
  const round: RoundInfo = { id: roundID, roundNo: roundNumberFromDir(jobContext.round_dir), startedAt, endedAt: null };
  const roundDir = jobContext.round_dir;
  await writeJobContext();
  const recordMetaPath = path.join(roundDir, recordMetaFileName);
  const recordMeta = await waitForRecordMeta(recordMetaPath, round.id);
  const mediaTimeZeroWallAt = new Date(recordMeta.media_time_zero_wall_at);
  if (!Number.isFinite(mediaTimeZeroWallAt.getTime())) {
    throw new Error(`invalid media_time_zero_wall_at in ${recordMetaPath}`);
  }
  const outputPath = path.join(roundDir, outputFileName);
  writer = new BilibiliXMLWriter(outputPath);
  await writer.open();

  const runtime = await connectLeanCloudWithRetry(chatRoomID, round);
  let stopSampling = () => {};
  if (runtime) {
    runtimeClient = runtime.client;
    room = runtime.room;
    stopSampling = startOnlineSampler(runtime.room, round);
  }
  await waitUntilStopped();
  stopSampling();
  const finalRound = { ...round, endedAt: new Date() };
  await writer.close();
  await stats.writeOutputs(roundDir, {
    roundNo: finalRound.roundNo,
    startedAt: finalRound.startedAt,
    endedAt: finalRound.endedAt,
    timebase: "record-video",
    recordMetaPath: recordMetaFileName,
    videoOffsetSeconds: danmuConf.VideoOffsetSeconds,
    mediaTimeZeroWallAt,
  });
  await writeJobResult(outputPath);
}

async function connectLeanCloudWithRetry(roomID: string, round: RoundInfo): Promise<LeanRuntime | null> {
  let attempt = 0;
  for (;;) {
    if (shuttingDown) {
      console.error(
        `leancloud room ${roomID} for round ${round.id} was not connected before shutdown after ${attempt} attempt(s)`,
      );
      return null;
    }
    attempt++;
    diagnostics.connectAttempts = attempt;
    try {
      return await connectLeanCloud(roomID, round);
    } catch (error) {
      diagnostics.lastConnectError = error instanceof Error ? error.message : String(error);
      const delayMs = Math.min(30000, 2000 * attempt);
      console.error(
        `connect leancloud room ${roomID} for round ${round.id} failed, retrying in ${delayMs}ms: ${
          error instanceof Error ? error.message : String(error)
        }`,
      );
      await sleep(delayMs);
    }
  }
}

async function connectLeanCloud(roomID: string, round: RoundInfo): Promise<LeanRuntime> {
  const { Event } = await loadLeancloudRuntime();
  const realtime = await getLeancloudRealtime();
  const clientID = `rm-monitor-danmu-${round.id}-${Date.now().toString(36)}`;
  const client = await withTimeout(
    realtime.createIMClient(clientID),
    leancloudConnectTimeoutMs,
    `create leancloud im client ${clientID}`,
  );
  registerRuntimeDiagnostics(client, "leancloud-client");
  const room = await withTimeout(resolveRoom(client, roomID), leancloudConnectTimeoutMs, `resolve leancloud room ${roomID}`);
  registerRuntimeDiagnostics(room, "leancloud-room");
  if (room?.join) {
    await withTimeout(room.join(), leancloudConnectTimeoutMs, `join leancloud room ${roomID}`);
  }
  diagnostics.connectedAt = new Date().toISOString();
  console.log(`connected leancloud room ${roomID} for round ${round.id} with client ${clientID}`);

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

  await withTimeout(
    writeHistory(room as { queryMessages?: (opts: unknown) => Promise<LeanMessage[]> }, round, seen, seenOrder),
    leancloudHistoryTimeoutMs,
    `query leancloud history ${roomID}`,
  );
  hydrating = false;
  pending.sort((a, b) => messageTimestamp(a) - messageTimestamp(b));
  for (const message of pending) {
    writeMessageIfValid(message, round, seen, seenOrder);
  }
  return { client: client as LeanRuntime["client"], room: room as LeanRuntime["room"] };
}

async function getLeancloudRealtime() {
  if (leancloudRealtime) {
    return leancloudRealtime;
  }
  const { Realtime } = await loadLeancloudRuntime();
  leancloudRealtime = new Realtime({
    appId: danmuConf.AppID,
    appKey: danmuConf.AppKey,
    server: {
      RTMRouter: "https://router-g0-push.leancloud.cn",
      api: "https://api.leancloud.cn",
    },
  });
  return leancloudRealtime;
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
  if (!Number.isFinite(timestamp)) {
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
  const offsetSeconds = videoOffsetSeconds(timestamp);
  if (offsetSeconds < 0) {
    return;
  }
  const item: DanmuMessage = {
    id,
    offsetSeconds,
    timestamp,
    text,
    username: String(attrs.username ?? ""),
    nickname: String(attrs.nickname ?? ""),
    attributes: attrs,
    mode: attrs.mode,
    color: attrs.color,
  };
  stats.recordDanmu(item.offsetSeconds);
  diagnostics.acceptedMessages++;
  writer?.writeMessage(item);
}

function startOnlineSampler(room: { count?: () => Promise<number> }, round: RoundInfo): () => void {
  let stopped = false;
  const sample = async () => {
    const offsetSeconds = videoOffsetSeconds(Date.now());
    if (offsetSeconds < 0) {
      return;
    }
    try {
      if (typeof room.count !== "function") {
        stats.recordOnline(offsetSeconds, null);
      } else {
        diagnostics.onlineSampleAttempts++;
        const count = await withTimeout(room.count(), leancloudOnlineCountTimeoutMs, "leancloud room count");
        stats.recordOnline(offsetSeconds, Number.isFinite(count) ? Number(count) : null);
        diagnostics.onlineSampleSuccess++;
      }
    } catch (error) {
      diagnostics.onlineSampleFailures++;
      if (error instanceof Error && error.message.includes("timed out")) {
        diagnostics.onlineSampleTimeouts++;
      }
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

function videoOffsetSeconds(timestampMs: number): number {
  return (timestampMs - mediaTimeZeroWallMs) / 1000 + danmuConf.VideoOffsetSeconds;
}

async function waitForRecordMeta(recordMetaPath: string, roundID: number): Promise<RecordMeta> {
  for (;;) {
    if (shuttingDown) {
      throw new Error("shutting down");
    }
    const meta = await tryReadRecordMeta(recordMetaPath);
    if (meta) {
      mediaTimeZeroWallMs = new Date(meta.media_time_zero_wall_at).getTime();
      return meta;
    }
    await sleep(500);
  }
}

async function tryReadRecordMeta(recordMetaPath: string): Promise<RecordMeta | null> {
  try {
    const raw = await fs.promises.readFile(recordMetaPath, "utf8");
    const meta = JSON.parse(raw) as RecordMeta;
    if (meta.schema !== "rm-monitor/record-meta/v1") {
      throw new Error(`unexpected record metadata schema ${meta.schema}`);
    }
    return meta;
  } catch (error: any) {
    if (error?.code === "ENOENT") {
      return null;
    }
    throw error;
  }
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

function registerRuntimeDiagnostics(target: any, label: string) {
  if (!target || typeof target.on !== "function") {
    return;
  }
  for (const eventName of ["disconnect", "reconnect", "offline", "online", "close", "error"]) {
    try {
      target.on(eventName, (error: unknown) => {
        if (eventName === "error") {
          console.error(`${label} ${eventName}: ${error instanceof Error ? error.message : String(error)}`);
          return;
        }
        console.error(`${label} event: ${eventName}`);
      });
    } catch {
      // Some SDK objects expose on() but reject unknown event names.
    }
  }
}

async function withTimeout<T>(promise: Promise<T>, timeoutMs: number, label: string): Promise<T> {
  let timer: ReturnType<typeof setTimeout> | undefined;
  try {
    return await Promise.race([
      promise,
      new Promise<T>((_, reject) => {
        timer = setTimeout(() => reject(new Error(`${label} timed out after ${timeoutMs}ms`)), timeoutMs);
      }),
    ]);
  } finally {
    if (timer) {
      clearTimeout(timer);
    }
  }
}

async function waitUntilStopped() {
  for (;;) {
    if (shuttingDown) {
      return;
    }
    await sleep(1000);
  }
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

function loadJobContext(): DanmuJobContext {
  const raw = process.env.RM_MONITOR_JOB_CONTEXT || "";
  if (!raw.trim()) {
    fatal("RM_MONITOR_JOB_CONTEXT is required");
  }
  return JSON.parse(raw) as DanmuJobContext;
}

function jobDir(): string {
  return path.join(jobContext.round_dir, ".job", `danmu-${roundID}`);
}

async function writeJobContext() {
  await atomicWriteJSON(path.join(jobDir(), "context.json"), jobContext);
}

async function writeJobResult(outputPath: string) {
  await atomicWriteJSON(path.join(jobDir(), "result.json"), {
    schema: "rm-monitor/danmu-result/v1",
    match_round_id: roundID,
    output_path: outputPath,
    diagnostics: {
      connected: diagnostics.connectedAt !== null,
      connected_at: diagnostics.connectedAt,
      connect_attempts: diagnostics.connectAttempts,
      last_connect_error: diagnostics.lastConnectError || undefined,
      accepted_messages: diagnostics.acceptedMessages,
      online_sample_attempts: diagnostics.onlineSampleAttempts,
      online_sample_success: diagnostics.onlineSampleSuccess,
      online_sample_failures: diagnostics.onlineSampleFailures,
      online_sample_timeouts: diagnostics.onlineSampleTimeouts,
    },
    completed_at: new Date().toISOString(),
  });
}

async function writeJobError(error: unknown) {
  if (!roundID || !jobContext.round_dir) {
    return;
  }
  await atomicWriteJSON(path.join(jobDir(), "error.json"), {
    schema: "rm-monitor/job-error/v1",
    job_type: "danmu",
    task_id: roundID,
    error_message: error instanceof Error ? error.message : String(error),
    failed_at: new Date().toISOString(),
  });
}

async function atomicWriteJSON(filePath: string, value: unknown) {
  await fs.promises.mkdir(path.dirname(filePath), { recursive: true });
  const tmp = `${filePath}.tmp-${process.pid}-${Date.now()}`;
  await fs.promises.writeFile(tmp, `${JSON.stringify(value, null, 2)}\n`, "utf8");
  await fs.promises.rename(tmp, filePath);
}

function roundNumberFromDir(roundDir: string): number {
  const base = path.basename(roundDir);
  const match = /^Round-(\d+)$/i.exec(base);
  return match ? Number(match[1]) : 0;
}

function fatal(message: string): never {
  console.error(message);
  process.exit(1);
}
