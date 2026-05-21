import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { strict as assert } from "node:assert";
import test from "node:test";
import { DanmuStats } from "./stats.js";

test("DanmuStats buckets danmu counts and writes JSON/PNG outputs", async () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "danmu-stats-"));
  const stats = new DanmuStats();
  stats.recordDanmu(1);
  stats.recordDanmu(9.9);
  stats.recordDanmu(10);
  stats.recordOnline(0, 12);
  stats.recordOnline(10, null);

  await stats.writeOutputs(dir, {
    roundNo: 1,
    startedAt: new Date("2026-05-20T10:00:00Z"),
    endedAt: new Date("2026-05-20T10:05:00Z"),
    timebase: "record-video",
    recordMetaPath: "record-meta.json",
    videoOffsetSeconds: -3,
    mediaTimeZeroWallAt: new Date("2026-05-20T09:59:58Z"),
  });

  const danmuRaw = fs.readFileSync(path.join(dir, "stats", "danmu-count.json"), "utf8");
  const danmu = JSON.parse(danmuRaw);
  assert.equal(danmu.schema, "rm-monitor/danmu-count/v1");
  assert.equal(danmu.timebase, "record-video");
  assert.equal(danmu.record_meta_path, "record-meta.json");
  assert.equal(danmu.video_offset_seconds, -3);
  assert.equal(danmu.media_time_zero_wall_at, "2026-05-20T09:59:58.000Z");
  assert.deepEqual(danmu.points, [
    { t: 0, count: 2, total: 2 },
    { t: 10, count: 1, total: 3 },
  ]);

  const online = JSON.parse(fs.readFileSync(path.join(dir, "stats", "online-count.json"), "utf8"));
  assert.deepEqual(online.points, [
    { t: 0, online_count: 12 },
    { t: 10, online_count: null },
  ]);

  for (const name of ["danmu-count.png", "online-count.png"]) {
    const png = fs.readFileSync(path.join(dir, "stats", name));
    assert.equal(png.subarray(0, 8).toString("hex"), "89504e470d0a1a0a");
    assert.ok(png.length > 1024);
  }
});
