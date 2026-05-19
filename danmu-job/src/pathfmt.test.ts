import { strict as assert } from "node:assert";
import test from "node:test";
import { recordConfWithDefaults } from "./config.js";
import { renderMatchDir, sanitizePath } from "./pathfmt.js";

test("renderMatchDir follows rm-monitor default templates", () => {
  const conf = recordConfWithDefaults({});
  const got = renderMatchDir(conf, {
    Event: "RMUC 2026超级对抗赛",
    Zone: "南部赛区",
    Order: 78,
    RedSchool: "仲恺农业工程学院",
    RedName: "奇点",
    BlueSchool: "华南农业大学",
    BlueName: "Taurus",
    RoundNo: 1,
    Role: "主视角",
  });
  assert.equal(got, "RMUC 2026超级对抗赛/南部赛区/78. 仲恺农业工程学院-奇点 VS 华南农业大学-Taurus");
});

test("sanitizePath replaces unsafe segment characters", () => {
  assert.equal(sanitizePath("a/b:c/<bad>?"), "a/b_c/_bad__");
});
