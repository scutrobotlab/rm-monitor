use anyhow::{anyhow, bail, Context, Result};
use chrono::{DateTime, Utc};
use quick_xml::events::{BytesDecl, BytesEnd, BytesStart, BytesText, Event};
use quick_xml::{Reader, Writer};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::fs;
use std::io::{Read, Write};
use std::path::{Path, PathBuf};
use std::process::Command;

const ENV_NAME: &str = "RM_MONITOR_JOB_CONTEXT";
const TEMP_JOB_DIR: &str = "/tmp/job";
const ARGO_OUT_DIR: &str = "/tmp/argo";

#[derive(Debug, Clone, Deserialize, Serialize)]
struct RecordTrimContext {
    schema: String,
    #[serde(default)]
    match_id: String,
    match_round_id: i64,
    #[serde(default)]
    round_no: i64,
    role: String,
    base_dir: String,
    round_dir: String,
    source_path: String,
    output_path: String,
    #[serde(default)]
    trim_sidecars: bool,
}

#[derive(Debug, Clone, Deserialize)]
struct RoundAnalysis {
    boundary: Boundary,
}

#[derive(Debug, Clone, Deserialize)]
struct Boundary {
    start_seconds: f64,
    end_seconds: f64,
}

#[derive(Debug, Serialize)]
struct RecordTrimResult {
    schema: &'static str,
    #[serde(skip_serializing_if = "String::is_empty")]
    match_id: String,
    match_round_id: i64,
    role: String,
    output_path: String,
    format: &'static str,
    codec: &'static str,
    file_size: u64,
    checksum: String,
    start_seconds: f64,
    end_seconds: f64,
    duration_seconds: f64,
    completed_at: DateTime<Utc>,
}

#[derive(Debug, Deserialize)]
struct Probe {
    #[serde(default)]
    streams: Vec<ProbeStream>,
    #[serde(default)]
    format: ProbeFormat,
}

#[derive(Debug, Deserialize)]
struct ProbeStream {
    codec_type: Option<String>,
}

#[derive(Debug, Default, Deserialize)]
struct ProbeFormat {
    duration: Option<String>,
}

fn main() {
    if let Err(err) = run() {
        let _ = write_error(&err);
        eprintln!("{err:?}");
        std::process::exit(1);
    }
}

fn run() -> Result<()> {
    let raw_ctx = std::env::var(ENV_NAME).with_context(|| format!("{ENV_NAME} is required"))?;
    let ctx: RecordTrimContext =
        serde_json::from_str(&raw_ctx).context("decode record trim context")?;
    fs::create_dir_all(TEMP_JOB_DIR).context("create temp job dir")?;
    write_json(Path::new(TEMP_JOB_DIR).join("context.json"), &ctx).context("write context")?;

    let boundary = read_boundary(&ctx)?;
    let duration = boundary.end_seconds - boundary.start_seconds;
    if duration <= 0.0 {
        bail!(
            "invalid round boundary: start={} end={}",
            boundary.start_seconds,
            boundary.end_seconds
        );
    }
    let (output_rel, output_path) = artifact_path(&ctx.base_dir, &ctx.output_path)?;
    let (_, source_path) = artifact_path(&ctx.base_dir, &ctx.source_path)?;
    if output_path.extension().and_then(|v| v.to_str()) != Some("mp4") {
        bail!("record-trim output must be .mp4: {}", output_path.display());
    }
    fs::create_dir_all(
        output_path
            .parent()
            .ok_or_else(|| anyhow!("output path has no parent"))?,
    )
    .context("create output dir")?;
    let _ = fs::remove_file(&output_path);

    let ffmpeg_args =
        build_ffmpeg_args(boundary.start_seconds, duration, &source_path, &output_path);
    run_command("ffmpeg", &ffmpeg_args).context("run ffmpeg copy remux")?;
    validate_output(&output_path, duration)?;
    if ctx.trim_sidecars {
        trim_danmu(
            &ctx.role,
            Path::new(&ctx.round_dir),
            boundary.start_seconds,
            boundary.end_seconds,
        )?;
    }

    let stat = fs::metadata(&output_path).context("stat output mp4")?;
    let result = RecordTrimResult {
        schema: "rm-monitor/record-trim-result/v1",
        match_id: ctx.match_id.clone(),
        match_round_id: ctx.match_round_id,
        role: ctx.role.clone(),
        output_path: output_rel,
        format: "mp4",
        codec: "copy",
        file_size: stat.len(),
        checksum: sha256_file(&output_path)?,
        start_seconds: boundary.start_seconds,
        end_seconds: boundary.end_seconds,
        duration_seconds: duration,
        completed_at: Utc::now(),
    };
    write_json(Path::new(TEMP_JOB_DIR).join("result.json"), &result).context("write result")?;
    write_argo_outputs(&[
        ("output_path", result.output_path.clone()),
        ("role", result.role.clone()),
        ("file_size", result.file_size.to_string()),
        ("checksum", result.checksum.clone()),
        ("start_seconds", format!("{:.3}", result.start_seconds)),
        ("end_seconds", format!("{:.3}", result.end_seconds)),
        (
            "duration_seconds",
            format!("{:.3}", result.duration_seconds),
        ),
    ])?;
    Ok(())
}

fn read_boundary(ctx: &RecordTrimContext) -> Result<Boundary> {
    let raw = fs::read_to_string(Path::new(&ctx.round_dir).join("round.json"))
        .context("read round analysis")?;
    let doc: RoundAnalysis = serde_json::from_str(&raw).context("parse round analysis")?;
    Ok(doc.boundary)
}

fn build_ffmpeg_args(start: f64, duration: f64, source: &Path, output: &Path) -> Vec<String> {
    vec![
        "-hide_banner".into(),
        "-nostdin".into(),
        "-fflags".into(),
        "+genpts+discardcorrupt".into(),
        "-err_detect".into(),
        "ignore_err".into(),
        "-ss".into(),
        format!("{start:.3}"),
        "-i".into(),
        source.display().to_string(),
        "-t".into(),
        format!("{duration:.3}"),
        "-map".into(),
        "0".into(),
        "-c".into(),
        "copy".into(),
        "-avoid_negative_ts".into(),
        "make_zero".into(),
        "-movflags".into(),
        "+faststart".into(),
        "-y".into(),
        output.display().to_string(),
    ]
}

fn validate_output(path: &Path, expected_duration: f64) -> Result<()> {
    let stat = fs::metadata(path).context("stat output mp4")?;
    if stat.len() == 0 {
        bail!("output mp4 is empty");
    }
    let probe = ffprobe(path)?;
    if !probe
        .streams
        .iter()
        .any(|s| s.codec_type.as_deref() == Some("video"))
    {
        bail!("output mp4 has no video stream");
    }
    let observed = probe
        .format
        .duration
        .as_deref()
        .and_then(|v| v.parse::<f64>().ok())
        .ok_or_else(|| anyhow!("ffprobe did not report duration"))?;
    let tolerance = 3.0_f64.max(expected_duration * 0.02);
    if (observed - expected_duration).abs() > tolerance {
        bail!(
            "output duration mismatch: observed={observed:.3} expected={expected_duration:.3} tolerance={tolerance:.3}"
        );
    }
    Ok(())
}

fn ffprobe(path: &Path) -> Result<Probe> {
    let output = Command::new("ffprobe")
        .args([
            "-v",
            "error",
            "-print_format",
            "json",
            "-show_streams",
            "-show_format",
        ])
        .arg(path)
        .output()
        .context("run ffprobe")?;
    if !output.status.success() {
        bail!(
            "ffprobe failed: {}",
            String::from_utf8_lossy(&output.stderr).trim()
        );
    }
    serde_json::from_slice(&output.stdout).context("parse ffprobe json")
}

fn trim_danmu(role: &str, round_dir: &Path, start: f64, end: f64) -> Result<()> {
    let raw_path = round_dir.join(format!("{role}.raw.danmuku.xml"));
    let final_path = round_dir.join(format!("{role}.danmuku.xml"));
    ensure_raw_source(&raw_path, &final_path).context("prepare raw danmu")?;
    let raw = match fs::read(&raw_path) {
        Ok(v) => v,
        Err(err) if err.kind() == std::io::ErrorKind::NotFound => return Ok(()),
        Err(err) => return Err(err).context("read raw danmu"),
    };
    let mut reader = Reader::from_reader(raw.as_slice());
    reader.trim_text(false);
    let mut writer = Writer::new_with_indent(Vec::new(), b' ', 2);
    writer
        .write_event(Event::Decl(BytesDecl::new("1.0", Some("UTF-8"), None)))
        .context("write danmu xml declaration")?;
    writer
        .write_event(Event::Start(BytesStart::new("i")))
        .context("write danmu root")?;

    let mut buf = Vec::new();
    loop {
        match reader
            .read_event_into(&mut buf)
            .context("parse danmu xml")?
        {
            Event::Start(e) if e.name().as_ref() == b"d" => {
                let attrs = danmu_attrs(&reader, &e)?;
                let text = reader
                    .read_text(e.name())
                    .context("read danmu text")?
                    .into_owned();
                if let Some(retimed_p) = retime_danmu_p(attrs.p.as_deref(), start, end) {
                    let mut tag = BytesStart::new("d");
                    for (key, value) in attrs.other {
                        tag.push_attribute((key.as_str(), value.as_str()));
                    }
                    tag.push_attribute(("p", retimed_p.as_str()));
                    writer
                        .write_event(Event::Start(tag))
                        .context("write danmu item")?;
                    writer
                        .write_event(Event::Text(BytesText::new(&text)))
                        .context("write danmu text")?;
                    writer
                        .write_event(Event::End(BytesEnd::new("d")))
                        .context("close danmu item")?;
                }
            }
            Event::Eof => break,
            _ => {}
        }
        buf.clear();
    }
    writer
        .write_event(Event::End(BytesEnd::new("i")))
        .context("close danmu root")?;
    let out = writer.into_inner();
    atomic_write(&final_path, &out).context("write final danmu")
}

#[derive(Debug, Default)]
struct DanmuAttrs {
    p: Option<String>,
    other: Vec<(String, String)>,
}

fn danmu_attrs(reader: &Reader<&[u8]>, event: &BytesStart<'_>) -> Result<DanmuAttrs> {
    let mut out = DanmuAttrs::default();
    for attr in event.attributes() {
        let attr = attr.context("read danmu attr")?;
        let key = std::str::from_utf8(attr.key.as_ref())
            .context("decode danmu attr key")?
            .to_string();
        let value = attr
            .decode_and_unescape_value(reader)
            .context("decode danmu attr value")?
            .into_owned();
        if key == "p" {
            out.p = Some(value);
        } else {
            out.other.push((key, value));
        }
    }
    Ok(out)
}

fn retime_danmu_p(p: Option<&str>, start: f64, end: f64) -> Option<String> {
    let mut parts: Vec<String> = p?.split(',').map(ToOwned::to_owned).collect();
    let first = parts.first_mut()?;
    let t = first.trim().parse::<f64>().ok()?;
    if t < start || t > end {
        return None;
    }
    *first = format!("{:.3}", t - start);
    Some(parts.join(","))
}


fn ensure_raw_source(raw_path: &Path, final_path: &Path) -> Result<()> {
    if raw_path.exists() {
        return Ok(());
    }
    match fs::rename(final_path, raw_path) {
        Ok(()) => Ok(()),
        Err(err) if err.kind() == std::io::ErrorKind::NotFound => Ok(()),
        Err(err) => Err(err).context("move final sidecar to raw"),
    }
}


fn artifact_path(base_dir: &str, artifact_path: &str) -> Result<(String, PathBuf)> {
    let base = normalize_slash(base_dir);
    if base == "." || base == "/" {
        bail!("invalid base dir {base_dir:?}");
    }
    let mut p = normalize_slash(artifact_path);
    if p == "." || p == "/" {
        bail!("artifact path is empty");
    }
    if p.starts_with('/') {
        let prefix = format!("{}/", base.trim_end_matches('/'));
        if !p.starts_with(&prefix) {
            bail!("artifact path {artifact_path:?} is outside base dir {base_dir:?}");
        }
        p = p[prefix.len()..].to_string();
    }
    if p == ".." || p.starts_with("../") {
        bail!("artifact path {artifact_path:?} escapes records root");
    }
    Ok((p.clone(), Path::new(base_dir).join(PathBuf::from(p))))
}

fn normalize_slash(value: &str) -> String {
    let value = value.trim().replace('\\', "/");
    let mut parts = Vec::new();
    let absolute = value.starts_with('/');
    for part in value.split('/') {
        if part.is_empty() || part == "." {
            continue;
        }
        if part == ".." {
            if parts.pop().is_none() {
                parts.push("..");
            }
            continue;
        }
        parts.push(part);
    }
    let joined = parts.join("/");
    if absolute {
        format!("/{joined}")
    } else if joined.is_empty() {
        ".".to_string()
    } else {
        joined
    }
}

fn run_command(program: &str, args: &[String]) -> Result<()> {
    let output = Command::new(program)
        .args(args)
        .output()
        .with_context(|| format!("run {program}"))?;
    if !output.status.success() {
        bail!(
            "{program} failed: {}",
            String::from_utf8_lossy(&output.stderr).trim()
        );
    }
    Ok(())
}

fn sha256_file(path: &Path) -> Result<String> {
    let mut file = fs::File::open(path).with_context(|| format!("open {}", path.display()))?;
    let mut hasher = Sha256::new();
    let mut buf = [0_u8; 64 * 1024];
    loop {
        let n = file
            .read(&mut buf)
            .with_context(|| format!("read {}", path.display()))?;
        if n == 0 {
            break;
        }
        hasher.update(&buf[..n]);
    }
    Ok(format!("{:x}", hasher.finalize()))
}

fn write_json<T: Serialize>(path: PathBuf, value: &T) -> Result<()> {
    let raw = serde_json::to_vec_pretty(value).context("encode json")?;
    let mut with_newline = raw;
    with_newline.push(b'\n');
    atomic_write(&path, &with_newline)
}

fn write_argo_outputs(values: &[(&str, String)]) -> Result<()> {
    let dir = Path::new(ARGO_OUT_DIR);
    fs::create_dir_all(dir).context("create argo output dir")?;
    for (name, value) in values {
        fs::write(dir.join(name), value).with_context(|| format!("write argo output {name}"))?;
    }
    Ok(())
}

fn write_error(err: &anyhow::Error) -> Result<()> {
    #[derive(Serialize)]
    struct ErrorResult<'a> {
        schema: &'static str,
        task_type: &'static str,
        status: &'static str,
        error_message: &'a str,
        completed_at: DateTime<Utc>,
    }
    fs::create_dir_all(TEMP_JOB_DIR).context("create temp job dir")?;
    let msg = err.to_string();
    write_json(
        Path::new(TEMP_JOB_DIR).join("error.json"),
        &ErrorResult {
            schema: "rm-monitor/job-error/v1",
            task_type: "record-trim",
            status: "FAILED",
            error_message: &msg,
            completed_at: Utc::now(),
        },
    )
}

fn atomic_write(path: &Path, data: &[u8]) -> Result<()> {
    if let Some(parent) = path.parent() {
        fs::create_dir_all(parent).with_context(|| format!("create {}", parent.display()))?;
    }
    let tmp = path.with_extension(format!(
        "{}.tmp",
        path.extension().and_then(|v| v.to_str()).unwrap_or("tmp")
    ));
    {
        let mut file =
            fs::File::create(&tmp).with_context(|| format!("write {}", tmp.display()))?;
        file.write_all(data)
            .with_context(|| format!("write {}", tmp.display()))?;
        file.sync_all().ok();
    }
    fs::rename(&tmp, path).with_context(|| format!("publish {}", path.display()))?;
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use tempfile::tempdir;

    #[test]
    fn reads_boundary() {
        let dir = tempdir().unwrap();
        fs::write(
            dir.path().join("round.json"),
            r#"{"boundary":{"start_seconds":3.5,"end_seconds":13.75}}"#,
        )
        .unwrap();
        let ctx = RecordTrimContext {
            schema: "rm-monitor/record-trim-context/v1".into(),
            match_id: "m".into(),
            match_round_id: 1,
            round_no: 1,
            role: "主视角".into(),
            base_dir: dir.path().display().to_string(),
            round_dir: dir.path().display().to_string(),
            source_path: "a.flv".into(),
            output_path: "a.mp4".into(),
            trim_sidecars: false,
        };
        let boundary = read_boundary(&ctx).unwrap();
        assert_eq!(boundary.start_seconds, 3.5);
        assert_eq!(boundary.end_seconds, 13.75);
    }

    #[test]
    fn builds_ffmpeg_args() {
        let args = build_ffmpeg_args(1.25, 8.5, Path::new("in.flv"), Path::new("out.mp4"));
        assert_eq!(
            args,
            vec![
                "-hide_banner",
                "-nostdin",
                "-fflags",
                "+genpts+discardcorrupt",
                "-err_detect",
                "ignore_err",
                "-ss",
                "1.250",
                "-i",
                "in.flv",
                "-t",
                "8.500",
                "-map",
                "0",
                "-c",
                "copy",
                "-avoid_negative_ts",
                "make_zero",
                "-movflags",
                "+faststart",
                "-y",
                "out.mp4"
            ]
        );
    }

    #[test]
    fn trims_danmu_sidecar() {
        let dir = tempdir().unwrap();
        fs::write(
            dir.path().join("主视角.danmuku.xml"),
            r#"<?xml version="1.0"?><i><d p="1.0,1,25">early</d><d p="5.0,1,25">keep</d><d p="12.0,1,25">late</d></i>"#,
        )
        .unwrap();

        trim_danmu("主视角", dir.path(), 3.0, 10.0).unwrap();

        let danmu = fs::read_to_string(dir.path().join("主视角.danmuku.xml")).unwrap();
        assert!(danmu.contains(r#"p="2.000,1,25""#));
        assert!(danmu.contains("keep"));
        assert!(!danmu.contains("early"));
        assert!(dir.path().join("主视角.raw.danmuku.xml").exists());
    }

    #[test]
    fn sidecar_trim_reads_existing_raw_and_moves_final_to_raw_when_missing() {
        let dir = tempdir().unwrap();
        fs::write(
            dir.path().join("主视角.raw.danmuku.xml"),
            r#"<?xml version="1.0"?><i><d p="5.0,1,25">from raw</d></i>"#,
        )
        .unwrap();
        fs::write(
            dir.path().join("主视角.danmuku.xml"),
            r#"<?xml version="1.0"?><i><d p="5.0,1,25">from final</d></i>"#,
        )
        .unwrap();
        trim_danmu("主视角", dir.path(), 3.0, 10.0).unwrap();
        let danmu = fs::read_to_string(dir.path().join("主视角.danmuku.xml")).unwrap();
        assert!(danmu.contains("from raw"));
        assert!(!danmu.contains("from final"));
    }
}
