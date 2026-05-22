use std::{
    fs::{self, File},
    io::Write,
    path::{Path, PathBuf},
    process::Stdio,
    time::Duration,
};

use anyhow::{bail, Context, Result};
use chrono::Utc;
use clap::Parser;
use danmu2ass::{AssWriter, CanvasConfig, Parser as DanmuParser};
use quick_xml::{
    events::{BytesEnd, BytesStart, BytesText, Event},
    Reader, Writer,
};
use serde::{Deserialize, Serialize};
use serde_json::Value;
use tokio::{process::Command, time::timeout};

#[derive(Parser)]
struct Args {
    #[arg(short = 'f', default_value = "etc/config.yml")]
    config: PathBuf,
}

#[derive(Debug, Deserialize)]
struct Config {
    #[serde(rename = "RecordConf", default)]
    record: RecordConf,
    #[serde(rename = "LLMConf", default)]
    llm: LLMConf,
}

#[derive(Debug, Default, Deserialize)]
struct RecordConf {
    #[serde(rename = "BaseDir", default = "default_base_dir")]
    base_dir: String,
}

fn default_base_dir() -> String {
    "/records".to_string()
}

#[derive(Debug, Default, Deserialize)]
struct LLMConf {
    #[serde(rename = "BaseURL", default)]
    base_url: String,
    #[serde(rename = "APIKey", default)]
    api_key: String,
    #[serde(rename = "Model", default)]
    model: String,
    #[serde(rename = "TimeoutSeconds", default = "default_timeout_seconds")]
    timeout_seconds: u64,
}

fn default_timeout_seconds() -> u64 {
    120
}

#[derive(Debug, Deserialize, Serialize)]
struct HighlightContext {
    highlight_clip_id: i32,
    highlight_index: i32,
    role: String,
    algorithm_version: String,
    start_seconds: f64,
    end_seconds: f64,
    peak_seconds: f64,
    score: f64,
    source_artifact_path: String,
    round_dir: String,
    output_dir: String,
    event: String,
    zone: String,
    order: i32,
    match_type: String,
    round_no: i32,
    red_school: String,
    red_name: String,
    blue_school: String,
    blue_name: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
struct STTLine {
    start: f64,
    end: f64,
    status: String,
    #[serde(default)]
    text: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
struct DanmuText {
    time: f64,
    text: String,
}

#[derive(Debug, Serialize, Deserialize)]
struct LLMOutput {
    title: String,
    description: String,
    tags: Vec<String>,
    #[serde(default)]
    explanation: String,
}

#[derive(Debug, Serialize)]
struct HighlightResult {
    highlight_clip_id: i32,
    output_dir: String,
    title: String,
    description: String,
    tags: Vec<String>,
    model_payload: Value,
    completed_at: chrono::DateTime<Utc>,
}

#[tokio::main]
async fn main() -> Result<()> {
    let args = Args::parse();
    let config: Config = serde_yaml::from_slice(
        &fs::read(&args.config)
            .with_context(|| format!("read config {}", args.config.display()))?,
    )?;
    let raw_context =
        std::env::var("RM_MONITOR_JOB_CONTEXT").context("RM_MONITOR_JOB_CONTEXT is required")?;
    let ctx: HighlightContext =
        serde_json::from_str(&raw_context).context("parse highlight context")?;
    let _ = write_context(&config, &ctx, &raw_context);
    if let Err(err) = run(&config, &ctx).await {
        let _ = write_error(&config, &ctx, &err.to_string());
        return Err(err);
    }
    Ok(())
}

async fn run(config: &Config, ctx: &HighlightContext) -> Result<()> {
    let base_dir = PathBuf::from(&config.record.base_dir);
    let source = resolve_under(&base_dir, &ctx.source_artifact_path)?;
    let round_dir = resolve_under(&base_dir, &ctx.round_dir)?;
    let output_dir = resolve_under(&base_dir, &ctx.output_dir)?;
    fs::create_dir_all(&output_dir).context("create highlight output dir")?;

    let stt_path = round_dir.join("stt.jsonl");
    let danmu_path = round_dir.join(format!("{}.danmuku.xml", ctx.role));
    ensure_file(&source)?;
    ensure_file(&stt_path)?;
    ensure_file(&danmu_path)?;

    let stt_lines = read_stt(&stt_path, ctx.start_seconds, ctx.end_seconds)?;
    let danmu_lines = read_danmu_text(&danmu_path, ctx.start_seconds, ctx.end_seconds)?;
    let llm = generate_llm(&config.llm, ctx, &stt_lines, &danmu_lines).await?;

    let video = output_dir.join("video.mp4");
    let danmu = output_dir.join("video.danmuku.xml");
    let srt = output_dir.join("video.srt");
    let ass = output_dir.join("video.danmuku.ass");
    let video_artifact = output_dir.join("video-artifact.mp4");
    let cover = output_dir.join("cover.jpg");

    slice_video(&source, &video, ctx.start_seconds, ctx.end_seconds).await?;
    crop_danmu(&danmu_path, &danmu, ctx.start_seconds, ctx.end_seconds)?;
    write_srt(&stt_path, &srt, ctx.start_seconds, ctx.end_seconds)?;
    render_ass(&danmu, &ass)?;
    burn_video(&video, &ass, &srt, &video_artifact).await?;
    extract_cover(
        &video,
        &cover,
        (ctx.peak_seconds - ctx.start_seconds).max(0.0),
    )
    .await?;

    let model_payload = serde_json::to_value(&llm)?;
    let highlight_json = build_highlight_json(ctx, &llm, &model_payload);
    atomic_write_json(&output_dir.join("highlight.json"), &highlight_json)?;
    atomic_write_json(
        &job_dir(&base_dir, ctx)?.join("result.json"),
        &HighlightResult {
            highlight_clip_id: ctx.highlight_clip_id,
            output_dir: ctx.output_dir.clone(),
            title: llm.title,
            description: llm.description,
            tags: llm.tags,
            model_payload,
            completed_at: Utc::now(),
        },
    )?;
    Ok(())
}

async fn slice_video(source: &Path, output: &Path, start: f64, end: f64) -> Result<()> {
    let tmp = output.with_extension("mp4.part");
    let _ = fs::remove_file(&tmp);
    let duration = (end - start).max(0.0);
    let status = Command::new("ffmpeg")
        .args(["-hide_banner", "-loglevel", "info", "-nostdin"])
        .args(["-ss", &format!("{start:.3}")])
        .arg("-i")
        .arg(source)
        .args([
            "-t",
            &format!("{duration:.3}"),
            "-map",
            "0:v:0",
            "-map",
            "0:a:0?",
            "-sn",
            "-dn",
            "-c",
            "copy",
            "-avoid_negative_ts",
            "make_zero",
            "-movflags",
            "+faststart",
            "-f",
            "mp4",
            "-y",
        ])
        .arg(&tmp)
        .stdout(Stdio::inherit())
        .stderr(Stdio::inherit())
        .status()
        .await
        .context("run ffmpeg slice")?;
    if !status.success() {
        let _ = fs::remove_file(&tmp);
        bail!("ffmpeg slice failed with {status}");
    }
    ensure_file(&tmp)?;
    validate_mp4(&tmp, duration).await?;
    fs::rename(tmp, output).context("publish highlight video")?;
    Ok(())
}

async fn validate_mp4(path: &Path, expected_duration: f64) -> Result<()> {
    let out = Command::new("ffprobe")
        .args([
            "-v",
            "error",
            "-show_entries",
            "format=duration",
            "-of",
            "default=noprint_wrappers=1:nokey=1",
        ])
        .arg(path)
        .output()
        .await
        .context("probe mp4")?;
    if !out.status.success() {
        bail!("ffprobe failed for {}", path.display());
    }
    let raw = String::from_utf8_lossy(&out.stdout);
    let duration: f64 = raw.trim().parse().context("parse mp4 duration")?;
    if duration <= 0.0 {
        bail!("mp4 has invalid duration {duration}");
    }
    if expected_duration > 0.0 && duration > expected_duration + 10.0 {
        bail!("mp4 duration {duration:.3}s exceeds expected {expected_duration:.3}s");
    }
    Ok(())
}

fn crop_danmu(input: &Path, output: &Path, start: f64, end: f64) -> Result<()> {
    let raw = fs::read(input).with_context(|| format!("read {}", input.display()))?;
    let tmp = output.with_extension("xml.part");
    let mut reader = Reader::from_reader(raw.as_slice());
    reader.trim_text(false);
    let mut writer = Writer::new(Vec::new());
    writer.write_event(Event::Decl(quick_xml::events::BytesDecl::new(
        "1.0",
        Some("UTF-8"),
        None,
    )))?;
    writer.write_event(Event::Start(BytesStart::new("i")))?;
    let mut buf = Vec::new();
    loop {
        match reader.read_event_into(&mut buf)? {
            Event::Start(e) if e.name().as_ref() == b"d" => {
                let mut elem = e.into_owned();
                let mut text = String::new();
                let mut inner = Vec::new();
                loop {
                    match reader.read_event_into(&mut inner)? {
                        Event::Text(t) => text.push_str(&t.unescape()?.into_owned()),
                        Event::End(end_tag) if end_tag.name().as_ref() == b"d" => break,
                        Event::Eof => bail!("unexpected EOF in danmu d element"),
                        _ => {}
                    }
                    inner.clear();
                }
                let p = attr_value(&elem, b"p")?;
                let Some(t) = parse_danmu_time(p.as_deref().unwrap_or("")) else {
                    continue;
                };
                if t < start || t > end {
                    continue;
                }
                rewrite_attr(
                    &mut elem,
                    "p",
                    &rewrite_danmu_time(p.as_deref().unwrap_or(""), t - start),
                )?;
                writer.write_event(Event::Start(elem))?;
                writer.write_event(Event::Text(BytesText::new(&text)))?;
                writer.write_event(Event::End(BytesEnd::new("d")))?;
            }
            Event::Eof => break,
            _ => {}
        }
        buf.clear();
    }
    writer.write_event(Event::End(BytesEnd::new("i")))?;
    atomic_write(&tmp, &writer.into_inner())?;
    fs::rename(tmp, output)?;
    Ok(())
}

fn read_danmu_text(input: &Path, start: f64, end: f64) -> Result<Vec<DanmuText>> {
    let raw = fs::read(input).with_context(|| format!("read {}", input.display()))?;
    let mut reader = Reader::from_reader(raw.as_slice());
    reader.trim_text(true);
    let mut buf = Vec::new();
    let mut out = Vec::new();
    loop {
        match reader.read_event_into(&mut buf)? {
            Event::Start(e) if e.name().as_ref() == b"d" => {
                let elem = e.into_owned();
                let p = attr_value(&elem, b"p")?;
                let mut text = String::new();
                let mut inner = Vec::new();
                loop {
                    match reader.read_event_into(&mut inner)? {
                        Event::Text(t) => text.push_str(&t.unescape()?.into_owned()),
                        Event::End(end_tag) if end_tag.name().as_ref() == b"d" => break,
                        Event::Eof => bail!("unexpected EOF in danmu d element"),
                        _ => {}
                    }
                    inner.clear();
                }
                if let Some(t) = parse_danmu_time(p.as_deref().unwrap_or("")) {
                    if t >= start && t <= end {
                        out.push(DanmuText { time: t, text });
                    }
                }
            }
            Event::Eof => break,
            _ => {}
        }
        buf.clear();
    }
    Ok(out)
}

fn attr_value(elem: &BytesStart<'_>, key: &[u8]) -> Result<Option<String>> {
    for attr in elem.attributes().with_checks(false) {
        let attr = attr?;
        if attr.key.as_ref() == key {
            return Ok(Some(attr.unescape_value()?.into_owned()));
        }
    }
    Ok(None)
}

fn rewrite_attr(elem: &mut BytesStart<'_>, key: &str, value: &str) -> Result<()> {
    let mut attrs = Vec::new();
    for attr in elem.attributes().with_checks(false) {
        let attr = attr?;
        let k = String::from_utf8_lossy(attr.key.as_ref()).to_string();
        if k != key {
            attrs.push((k, attr.unescape_value()?.into_owned()));
        }
    }
    elem.clear_attributes();
    for (k, v) in attrs {
        elem.push_attribute((k.as_str(), v.as_str()));
    }
    elem.push_attribute((key, value));
    Ok(())
}

fn parse_danmu_time(p: &str) -> Option<f64> {
    p.split(',').next()?.parse().ok()
}

fn rewrite_danmu_time(p: &str, t: f64) -> String {
    let mut parts: Vec<String> = p.split(',').map(str::to_string).collect();
    if parts.is_empty() {
        return format!("{:.3}", t.max(0.0));
    }
    parts[0] = format!("{:.3}", t.max(0.0));
    parts.join(",")
}

fn read_stt(path: &Path, start: f64, end: f64) -> Result<Vec<STTLine>> {
    let raw = fs::read_to_string(path).with_context(|| format!("read {}", path.display()))?;
    let mut all = Vec::new();
    let mut out = Vec::new();
    for line in raw.lines() {
        if line.trim().is_empty() {
            continue;
        }
        let row: STTLine = match serde_json::from_str(line) {
            Ok(v) => v,
            Err(_) => continue,
        };
        if row.status != "SUCCEEDED" || row.text.trim().is_empty() {
            continue;
        }
        if row.end >= start && row.start <= end {
            out.push(row.clone());
        }
        all.push(row);
    }
    if !out.is_empty() {
        return Ok(out);
    }
    if all.is_empty() {
        bail!("no stt text available");
    }
    let mut context = Vec::new();
    for row in &all {
        if row.end >= start - 60.0 && row.start <= end + 60.0 {
            context.push(row.clone());
        }
    }
    if !context.is_empty() {
        return Ok(context);
    }
    all.sort_by(|a, b| {
        let center = (start + end) / 2.0;
        let da = (((a.start + a.end) / 2.0) - center).abs();
        let db = (((b.start + b.end) / 2.0) - center).abs();
        da.partial_cmp(&db).unwrap_or(std::cmp::Ordering::Equal)
    });
    all.truncate(6);
    Ok(all)
}

fn write_srt(stt_path: &Path, output: &Path, start: f64, end: f64) -> Result<()> {
    let raw = fs::read_to_string(stt_path)?;
    let mut rows = Vec::new();
    for line in raw.lines() {
        if line.trim().is_empty() {
            continue;
        }
        let row: STTLine = match serde_json::from_str(line) {
            Ok(v) => v,
            Err(_) => continue,
        };
        if row.status == "SUCCEEDED"
            && !row.text.trim().is_empty()
            && row.end >= start
            && row.start <= end
        {
            rows.push(row);
        }
    }
    if rows.is_empty() {
        bail!("no stt subtitle in highlight window");
    }
    let mut body = String::new();
    for (idx, row) in rows.iter().enumerate() {
        let s = (row.start - start).max(0.0);
        let e = (row.end - start).max(s + 0.2);
        body.push_str(&format!(
            "{}\n{} --> {}\n{}\n\n",
            idx + 1,
            format_srt_time(s),
            format_srt_time(e),
            row.text.trim()
        ));
    }
    atomic_write(output, body.as_bytes())
}

fn format_srt_time(t: f64) -> String {
    let total_ms = (t.max(0.0) * 1000.0).round() as u64;
    let ms = total_ms % 1000;
    let total_s = total_ms / 1000;
    let s = total_s % 60;
    let total_m = total_s / 60;
    let m = total_m % 60;
    let h = total_m / 60;
    format!("{h:02}:{m:02}:{s:02},{ms:03}")
}

fn render_ass(input: &Path, output: &Path) -> Result<()> {
    let tmp = output.with_extension("ass.part");
    let canvas_config = CanvasConfig {
        duration: 15.0,
        width: 1920,
        height: 1080,
        font: "Noto Sans CJK SC".to_string(),
        font_size: 36,
        width_ratio: 1.2,
        horizontal_gap: 20.0,
        lane_size: 48,
        float_percentage: 0.55,
        bottom_percentage: 0.3,
        opacity: ((1.0 - 0.72) * 255.0) as u8,
        bold: 1,
        outline: 1.0,
        time_offset: 0.0,
    };
    let parser = DanmuParser::from_path(input)?;
    let mut writer = AssWriter::new(
        File::create(&tmp)?,
        "danmu".to_string(),
        canvas_config.clone(),
    )?;
    let mut canvas = canvas_config.canvas();
    for danmu in parser {
        if let Some(drawable) = canvas.draw(danmu?)? {
            writer.write(drawable)?;
        }
    }
    drop(writer);
    fs::rename(tmp, output)?;
    Ok(())
}

async fn burn_video(video: &Path, ass: &Path, srt: &Path, output: &Path) -> Result<()> {
    let tmp = output.with_extension("mp4.part");
    let filter = format!("ass={},subtitles={}", filter_path(ass), filter_path(srt));
    let status = timeout(
        Duration::from_secs(3600),
        Command::new("ffmpeg")
            .args(["-hide_banner", "-loglevel", "info", "-nostdin"])
            .arg("-i")
            .arg(video)
            .args([
                "-vf", &filter, "-map", "0:v:0", "-map", "0:a:0?", "-sn", "-dn",
            ])
            .args([
                "-c:v", "libx264", "-preset", "veryfast", "-crf", "20", "-pix_fmt", "yuv420p",
            ])
            .args([
                "-c:a",
                "aac",
                "-b:a",
                "128k",
                "-movflags",
                "+faststart",
                "-f",
                "mp4",
                "-y",
            ])
            .arg(&tmp)
            .stdout(Stdio::inherit())
            .stderr(Stdio::inherit())
            .status(),
    )
    .await
    .context("ffmpeg burn timeout")??;
    if !status.success() {
        let _ = fs::remove_file(&tmp);
        bail!("ffmpeg burn failed with {status}");
    }
    ensure_file(&tmp)?;
    fs::rename(tmp, output)?;
    Ok(())
}

async fn extract_cover(video: &Path, cover: &Path, seconds: f64) -> Result<()> {
    let tmp = cover.with_extension("part.jpg");
    let status = Command::new("ffmpeg")
        .args([
            "-hide_banner",
            "-loglevel",
            "info",
            "-nostdin",
            "-ss",
            &format!("{seconds:.3}"),
        ])
        .arg("-i")
        .arg(video)
        .args(["-frames:v", "1", "-q:v", "2", "-y"])
        .arg(&tmp)
        .stdout(Stdio::inherit())
        .stderr(Stdio::inherit())
        .status()
        .await?;
    if !status.success() {
        let _ = fs::remove_file(&tmp);
        bail!("ffmpeg cover failed with {status}");
    }
    ensure_file(&tmp)?;
    fs::rename(tmp, cover)?;
    Ok(())
}

async fn generate_llm(
    conf: &LLMConf,
    ctx: &HighlightContext,
    stt: &[STTLine],
    danmu: &[DanmuText],
) -> Result<LLMOutput> {
    if conf.base_url.trim().is_empty()
        || conf.api_key.trim().is_empty()
        || conf.model.trim().is_empty()
    {
        bail!("highlight llm config is incomplete");
    }
    let client = reqwest::Client::builder()
        .timeout(Duration::from_secs(conf.timeout_seconds.max(1)))
        .build()?;
    let input = serde_json::json!({
        "start_seconds": ctx.start_seconds,
        "end_seconds": ctx.end_seconds,
        "peak_seconds": ctx.peak_seconds,
        "score": ctx.score,
        "stt": stt,
        "danmu": danmu,
    });
    let url = format!("{}/chat/completions", conf.base_url.trim_end_matches('/'));
    let response: Value = client
        .post(url)
        .bearer_auth(&conf.api_key)
        .json(&serde_json::json!({
            "model": conf.model,
            "temperature": 0.2,
            "messages": [
                {"role": "system", "content": "你是 RoboMaster 赛事短视频编辑。只输出 JSON，对象字段为 title、description、tags、explanation。title 不超过 20 个中文字符，description 不超过 80 个中文字符，tags 是中文短标签数组。不要编造输入没有的击杀、战术或比分。"},
                {"role": "user", "content": input.to_string()}
            ]
        }))
        .send()
        .await?
        .error_for_status()?
        .json()
        .await?;
    let content = response
        .pointer("/choices/0/message/content")
        .and_then(Value::as_str)
        .context("llm returned no content")?;
    let out: LLMOutput =
        serde_json::from_str(&strip_code_fence(content)).context("parse highlight llm json")?;
    if out.title.trim().is_empty() || out.description.trim().is_empty() {
        bail!("highlight llm output missing title or description");
    }
    Ok(out)
}

fn strip_code_fence(input: &str) -> String {
    let s = input.trim();
    if !s.starts_with("```") {
        return s.to_string();
    }
    let lines: Vec<&str> = s.lines().collect();
    if lines.len() >= 3 {
        lines[1..lines.len() - 1].join("\n").trim().to_string()
    } else {
        s.to_string()
    }
}

fn build_highlight_json(ctx: &HighlightContext, llm: &LLMOutput, model_payload: &Value) -> Value {
    serde_json::json!({
        "schema": "rm-monitor/highlight/v1",
        "highlight_clip_id": ctx.highlight_clip_id,
        "highlight_index": ctx.highlight_index,
        "role": ctx.role,
        "algorithm_version": ctx.algorithm_version,
        "start_seconds": ctx.start_seconds,
        "end_seconds": ctx.end_seconds,
        "peak_seconds": ctx.peak_seconds,
        "score": ctx.score,
        "title": llm.title,
        "description": llm.description,
        "tags": llm.tags,
        "explanation": llm.explanation,
        "source_artifact": ctx.source_artifact_path,
        "output_dir": ctx.output_dir,
        "video": format!("{}/video.mp4", ctx.output_dir),
        "danmu": format!("{}/video.danmuku.xml", ctx.output_dir),
        "subtitle": format!("{}/video.srt", ctx.output_dir),
        "artifacts": {
            "video_artifact": format!("{}/video-artifact.mp4", ctx.output_dir),
            "cover": format!("{}/cover.jpg", ctx.output_dir)
        },
        "model_payload": model_payload,
        "publish": {"status": "disabled"}
    })
}

fn resolve_under(base: &Path, rel: &str) -> Result<PathBuf> {
    let mut clean = rel.trim().replace('\\', "/");
    let base_str = base.to_string_lossy().replace('\\', "/");
    if clean.starts_with(&format!("{}/", base_str.trim_end_matches('/'))) {
        clean = clean[base_str.trim_end_matches('/').len() + 1..].to_string();
    }
    clean = clean.trim_start_matches('/').to_string();
    if clean == ".." || clean.contains("../") {
        bail!("path escapes base dir: {rel}");
    }
    Ok(base.join(clean))
}

fn filter_path(path: &Path) -> String {
    path.to_string_lossy()
        .replace('\\', "\\\\")
        .replace(':', "\\:")
        .replace('\'', "\\'")
        .replace(',', "\\,")
}

fn ensure_file(path: &Path) -> Result<()> {
    let meta = fs::metadata(path).with_context(|| format!("stat {}", path.display()))?;
    if !meta.is_file() || meta.len() == 0 {
        bail!("file is missing or empty: {}", path.display());
    }
    Ok(())
}

fn atomic_write_json<T: Serialize>(path: &Path, value: &T) -> Result<()> {
    let raw = serde_json::to_vec_pretty(value)?;
    atomic_write(path, &raw)
}

fn atomic_write(path: &Path, data: &[u8]) -> Result<()> {
    if let Some(parent) = path.parent() {
        fs::create_dir_all(parent)?;
    }
    let tmp = path.with_extension("tmp");
    let mut f = File::create(&tmp)?;
    f.write_all(data)?;
    f.sync_all()?;
    drop(f);
    fs::rename(tmp, path)?;
    Ok(())
}

fn write_error(config: &Config, ctx: &HighlightContext, msg: &str) -> Result<()> {
    let base_dir = Path::new(&config.record.base_dir);
    let dir = job_dir(base_dir, ctx)?;
    atomic_write_json(
        &dir.join("error.json"),
        &serde_json::json!({
            "status": "failed",
            "task_type": "highlight",
            "highlight_clip_id": ctx.highlight_clip_id,
            "error_message": msg.chars().take(2000).collect::<String>(),
            "completed_at": Utc::now(),
        }),
    )
}

fn write_context(config: &Config, ctx: &HighlightContext, raw_context: &str) -> Result<()> {
    let base_dir = Path::new(&config.record.base_dir);
    let dir = job_dir(base_dir, ctx)?;
    let parsed: Value = serde_json::from_str(raw_context)?;
    atomic_write_json(&dir.join("context.json"), &parsed)
}

fn job_dir(base_dir: &Path, ctx: &HighlightContext) -> Result<PathBuf> {
    Ok(resolve_under(base_dir, &ctx.output_dir)?
        .join(".job")
        .join(format!("highlight-{}", ctx.highlight_clip_id)))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn rewrites_danmu_time() {
        assert_eq!(
            rewrite_danmu_time("12.5,1,25,0,0,0,0,0", 2.0),
            "2.000,1,25,0,0,0,0,0"
        );
    }

    #[test]
    fn formats_srt_time() {
        assert_eq!(format_srt_time(3661.234), "01:01:01,234");
    }
}
