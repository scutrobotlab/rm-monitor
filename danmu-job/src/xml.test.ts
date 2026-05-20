import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { strict as assert } from "node:assert";
import test from "node:test";
import { BilibiliXMLWriter, escapeXML, renderCustomAttributes, toBilibiliColor, toBilibiliMode } from "./xml.js";

test("escapeXML escapes user danmu text", () => {
  assert.equal(escapeXML(`a<&>"'`), "a&lt;&amp;&gt;&quot;&apos;");
});

test("mode and color convert to bilibili XML values", () => {
  assert.equal(toBilibiliMode(0), 1);
  assert.equal(toBilibiliMode(1), 5);
  assert.equal(toBilibiliMode(2), 4);
  assert.equal(toBilibiliColor("#ff0000"), 16711680);
  assert.equal(toBilibiliColor("bad"), 16777215);
});

test("custom attributes preserve LeanCloud attrs as flattened lc-prefixed XML attributes", () => {
  const attrs = renderCustomAttributes({
    "rmlive:msg_type": "danmu",
    nickname: `小<&>"'`,
    schoolName: "华南理工大学",
    racingAge: "2025",
    isAdmin: false,
    profile: { level: 3 },
  });
  assert.deepEqual(attrs, {
    lc_rmlive_msg_type: "danmu",
    lc_nickname: `小<&>"'`,
    lc_schoolName: "华南理工大学",
    lc_racingAge: "2025",
    lc_isAdmin: "false",
    lc_profile: "{\"level\":3}",
  });
});

test("BilibiliXMLWriter writes valid wrapper and atomic final file", async () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "danmu-"));
  const file = path.join(dir, "主视角.danmuku.xml");
  const writer = new BilibiliXMLWriter(file);
  await writer.open();
  writer.writeMessage({
    id: "m1",
    offsetSeconds: 1.25,
    timestamp: 1710000000000,
    text: "测试<弹幕>",
    mode: 0,
    color: "#ffffff",
    attributes: {
      nickname: "橘子",
      schoolName: "SCUT",
      racingAge: "2024",
      isAdmin: false,
    },
  });
  await writer.close();
  const raw = fs.readFileSync(file, "utf8");
  assert.match(raw, /<i>/);
  assert.match(raw, /<\/i>/);
  assert.match(raw, /1\.250,1,25,16777215/);
  assert.match(raw, /lc_schoolName="SCUT"/);
  assert.match(raw, /lc_racingAge="2024"/);
  assert.match(raw, /lc_nickname="橘子"/);
  assert.match(raw, /测试&lt;弹幕&gt;/);
  assert.equal(fs.existsSync(`${file}.tmp`), false);
});
