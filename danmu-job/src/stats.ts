import path from "node:path";
import * as VChartModule from "@visactor/vchart";
import * as Canvas from "canvas";
import { writeFileAtomic } from "./atomic.js";

export const statsBucketSeconds = 10;
const chartFontFamily = "Noto Sans CJK SC, Noto Sans CJK, Noto Sans SC, sans-serif";

export type DanmuCountPoint = {
  t: number;
  count: number;
  total: number;
};

export type OnlineCountPoint = {
  t: number;
  online_count: number | null;
};

type StatsMeta = {
  roundNo: number;
  startedAt: Date;
  endedAt: Date | null;
};

export class DanmuStats {
  private bucketCounts = new Map<number, number>();
  private total = 0;
  private onlinePoints: OnlineCountPoint[] = [];

  recordDanmu(offsetSeconds: number) {
    const bucket = Math.max(0, Math.floor(offsetSeconds / statsBucketSeconds) * statsBucketSeconds);
    this.bucketCounts.set(bucket, (this.bucketCounts.get(bucket) || 0) + 1);
    this.total += 1;
  }

  recordOnline(offsetSeconds: number, onlineCount: number | null) {
    const t = Math.max(0, Math.floor(offsetSeconds / statsBucketSeconds) * statsBucketSeconds);
    this.onlinePoints.push({ t, online_count: Number.isFinite(onlineCount) ? onlineCount : null });
  }

  danmuPoints(): DanmuCountPoint[] {
    const buckets = Array.from(this.bucketCounts.keys()).sort((a, b) => a - b);
    let running = 0;
    return buckets.map((t) => {
      const count = this.bucketCounts.get(t) || 0;
      running += count;
      return { t, count, total: running };
    });
  }

  onlineCountPoints(): OnlineCountPoint[] {
    const byBucket = new Map<number, number | null>();
    for (const point of this.onlinePoints) {
      byBucket.set(point.t, point.online_count);
    }
    return Array.from(byBucket.entries())
      .sort(([a], [b]) => a - b)
      .map(([t, online_count]) => ({ t, online_count }));
  }

  async writeOutputs(roundDir: string, meta: StatsMeta) {
    const statsDir = path.join(roundDir, "stats");
    const danmuPoints = this.danmuPoints();
    const onlinePoints = this.onlineCountPoints();
    await writeFileAtomic(path.join(statsDir, "danmu-count.json"), JSON.stringify(statsPayload(meta, "danmu-count", danmuPoints), null, 2));
    await writeFileAtomic(path.join(statsDir, "online-count.json"), JSON.stringify(statsPayload(meta, "online-count", onlinePoints), null, 2));
    await writeFileAtomic(path.join(statsDir, "danmu-count.png"), await renderLineChart({
      title: "弹幕数量",
      yName: "条",
      seriesName: "累计弹幕",
      points: danmuPoints.map((p) => [p.t, p.total]),
    }));
    await writeFileAtomic(path.join(statsDir, "online-count.png"), await renderLineChart({
      title: "在线人数",
      yName: "人",
      seriesName: "在线人数",
      points: onlinePoints.filter((p) => p.online_count !== null).map((p) => [p.t, p.online_count as number]),
    }));
  }
}

function statsPayload(meta: StatsMeta, kind: string, points: unknown[]) {
  return {
    schema: `rm-monitor/${kind}/v1`,
    round_no: meta.roundNo,
    started_at: meta.startedAt.toISOString(),
    ended_at: meta.endedAt?.toISOString() ?? null,
    bucket_seconds: statsBucketSeconds,
    points,
  };
}

async function renderLineChart(input: { title: string; yName: string; seriesName: string; points: number[][] }): Promise<Buffer> {
  const values = input.points.map(([t, value]) => ({ t, value }));
  const spec: any = {
    type: "line",
    width: 960,
    height: 360,
    backgroundColor: "#ffffff",
    title: {
      text: input.title,
      orient: "top",
      align: "left",
      textStyle: { fill: "#111827", fontSize: 20, fontWeight: 700, fontFamily: chartFontFamily },
    },
    data: [{ id: "stats", values }],
    xField: "t",
    yField: "value",
    point: { visible: values.length <= 40 },
    line: { style: { stroke: "#2563eb", lineWidth: 3 } },
    area: { visible: true, style: { fill: "#2563eb", fillOpacity: 0.12 } },
    axes: [
      {
        orient: "bottom",
        type: "linear",
        title: { visible: true, text: "时间 / 秒", style: { fontFamily: chartFontFamily } },
        label: { style: { fill: "#4b5563", fontFamily: chartFontFamily } },
        grid: { visible: true, style: { stroke: "#e5e7eb" } },
      },
      {
        orient: "left",
        type: "linear",
        title: { visible: true, text: input.yName, style: { fontFamily: chartFontFamily } },
        label: { style: { fill: "#4b5563", fontFamily: chartFontFamily } },
        grid: { visible: true, style: { stroke: "#e5e7eb" } },
      },
    ],
    legends: { visible: false },
    animation: false,
  };
  const VChartCtor =
    (VChartModule as any).default?.default || (VChartModule as any).default?.VChart || (VChartModule as any).VChart;
  const chart = new VChartCtor(spec, {
    mode: "node",
    modeParams: Canvas,
    animation: false,
  });
  chart.renderSync();
  const buffer = chart.getImageBuffer();
  chart.release();
  return Buffer.from(buffer);
}
