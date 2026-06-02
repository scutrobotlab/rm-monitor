use anyhow::{anyhow, bail, Context, Result};
use chrono::{DateTime, Utc};
use clap::Parser;
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::fs;
use std::io::{Read, Write};
use std::path::{Path, PathBuf};
use std::process::Command;

const ENV_NAME: &str = "RM_MONITOR_JOB_CONTEXT";
const TEMP_JOB_DIR: &str = "/tmp/job";
const ARGO_OUT_DIR: &str = "/tmp/argo";

#[derive(Debug, Parser)]
struct Args {
    #[arg(short = 'f', default_value = "etc/config.yml")]
    config: PathBuf,
}

#[derive(Debug, Clone, Deserialize, Serialize)]
struct TranscodeContext {
    schema: String,
    #[serde(default)]
    match_id: String,
    #[serde(default)]
    match_round_id: i64,
    #[serde(default)]
    round_no: i64,
    source_path: String,
    #[serde(default)]
    archive_path: String,
    #[serde(default)]
    base_dir: String,
    #[serde(default)]
    source_retention_days: i64,
    #[serde(default)]
    round_dir: String,
    #[serde(default)]
    role: String,
}

#[derive(Debug, Clone, Default, Deserialize)]
struct Config {
    #[serde(default, rename = "TranscodeConf")]
    transcode_conf: TranscodeConf,
}

#[derive(Debug, Clone, Deserialize)]
struct TranscodeConf {
    #[serde(default, rename = "BaseDir")]
    base_dir: String,
    #[serde(default, rename = "SourceRetentionDays")]
    source_retention_days: i64,
    #[serde(default, rename = "AV1Preset")]
    av1_preset: i64,
    #[serde(default, rename = "AV1Bitrate")]
    av1_bitrate: String,
    #[serde(default, rename = "AV1GOP")]
    av1_gop: i64,
    #[serde(default, rename = "AV1PixFmt")]
    av1_pix_fmt: String,
}

impl Default for TranscodeConf {
    fn default() -> Self {
        Self {
            base_dir: "/records".to_string(),
            source_retention_days: 7,
            av1_preset: 8,
            av1_bitrate: "1000k".to_string(),
            av1_gop: 125,
            av1_pix_fmt: "yuv420p".to_string(),
        }
    }
}

impl TranscodeConf {
    fn with_defaults(mut self) -> Self {
        let defaults = Self::default();
        if self.base_dir.trim().is_empty() {
            self.base_dir = defaults.base_dir;
        }
        if self.source_retention_days <= 0 {
            self.source_retention_days = defaults.source_retention_days;
        }
        if self.av1_preset <= 0 {
            self.av1_preset = defaults.av1_preset;
        }
        if self.av1_bitrate.trim().is_empty() {
            self.av1_bitrate = defaults.av1_bitrate;
        }
        if self.av1_gop <= 0 {
            self.av1_gop = defaults.av1_gop;
        }
        if self.av1_pix_fmt.trim().is_empty() {
            self.av1_pix_fmt = defaults.av1_pix_fmt;
        }
        self
    }
}

#[derive(Debug, Serialize)]
struct TranscodeResult {
    schema: &'static str,
    #[serde(skip_serializing_if = "String::is_empty")]
    match_id: String,
    match_round_id: i64,
    archive_path: String,
    format: &'static str,
    codec: &'static str,
    file_size: u64,
    checksum: String,
    completed_at: DateTime<Utc>,
}

fn main() {
    if let Err(err) = run() {
        let _ = write_error(&err);
        eprintln!("{err:?}");
        std::process::exit(1);
    }
}

fn run() -> Result<()> {
    let args = Args::parse();
    let conf = load_config(&args.config)?;
    let raw_ctx = std::env::var(ENV_NAME).with_context(|| format!("{ENV_NAME} is required"))?;
    let mut ctx: TranscodeContext =
        serde_json::from_str(&raw_ctx).context("decode transcode context")?;
    if ctx.base_dir.trim().is_empty() {
        ctx.base_dir = conf.base_dir.clone();
    }
    if ctx.source_retention_days <= 0 {
        ctx.source_retention_days = conf.source_retention_days;
    }
    fs::create_dir_all(TEMP_JOB_DIR).context("create temp job dir")?;
    write_json(Path::new(TEMP_JOB_DIR).join("context.json"), &ctx).context("write context")?;

    let (source_rel, source_path) = artifact_path(&ctx.base_dir, &ctx.source_path)?;
    if Path::new(&source_rel).extension().and_then(|v| v.to_str()) != Some("mp4") {
        bail!("transcode source must be .mp4: {source_rel}");
    }
    let archive_input = if ctx.archive_path.trim().is_empty() {
        source_rel.clone()
    } else {
        ctx.archive_path.clone()
    };
    let (archive_rel, archive_path) = artifact_path(&ctx.base_dir, &archive_input)?;
    if Path::new(&archive_rel).extension().and_then(|v| v.to_str()) != Some("mp4") {
        bail!("transcode archive must be .mp4: {archive_rel}");
    }
    let av1_rel = av1_archive_path(&archive_rel);
    let (_, av1_path) = artifact_path(&ctx.base_dir, &av1_rel)?;
    fs::create_dir_all(
        av1_path
            .parent()
            .ok_or_else(|| anyhow!("av1 path has no parent"))?,
    )
    .context("create archive dir")?;
    let _ = fs::remove_file(&av1_path);

    ensure_svt_av1()?;
    let ffmpeg_args = transcode_ffmpeg_args(&source_path, &av1_path, &conf);
    run_command("ffmpeg", &ffmpeg_args).context("run ffmpeg transcode")?;
    let (file_size, checksum) = validate_av1_output(&av1_path)?;
    replace_archive_with_av1(&archive_path, &av1_path)?;
    let published = fs::metadata(&archive_path).context("stat published archive")?;
    if published.len() != file_size {
        bail!(
            "published archive size mismatch: av1={} published={}",
            file_size,
            published.len()
        );
    }

    let result = TranscodeResult {
        schema: "rm-monitor/transcode-result/v1",
        match_id: ctx.match_id,
        match_round_id: ctx.match_round_id,
        archive_path: archive_rel,
        format: "mp4",
        codec: "av1",
        file_size,
        checksum,
        completed_at: Utc::now(),
    };
    write_json(Path::new(TEMP_JOB_DIR).join("result.json"), &result).context("write result")?;
    write_argo_outputs(&[
        ("archive_path", result.archive_path.clone()),
        ("format", result.format.to_string()),
        ("codec", result.codec.to_string()),
        ("file_size", result.file_size.to_string()),
        ("checksum", result.checksum.clone()),
    ])
}

fn load_config(path: &Path) -> Result<TranscodeConf> {
    let raw = fs::read_to_string(path).with_context(|| format!("read {}", path.display()))?;
    let config: Config = serde_yaml::from_str(&raw).context("parse config")?;
    Ok(config.transcode_conf.with_defaults())
}

fn transcode_ffmpeg_args(source: &Path, output: &Path, conf: &TranscodeConf) -> Vec<String> {
    let conf = conf.clone().with_defaults();
    vec![
        "-hide_banner".into(),
        "-loglevel".into(),
        "info".into(),
        "-i".into(),
        source.display().to_string(),
        "-map".into(),
        "0:v:0".into(),
        "-an".into(),
        "-sn".into(),
        "-dn".into(),
        "-c:v".into(),
        "libsvtav1".into(),
        "-preset".into(),
        conf.av1_preset.to_string(),
        "-b:v".into(),
        conf.av1_bitrate,
        "-g".into(),
        conf.av1_gop.to_string(),
        "-pix_fmt".into(),
        conf.av1_pix_fmt,
        "-movflags".into(),
        "+faststart".into(),
        "-f".into(),
        "mp4".into(),
        "-y".into(),
        output.display().to_string(),
    ]
}

fn av1_archive_path(archive_rel: &str) -> String {
    let path = Path::new(archive_rel);
    match path.extension().and_then(|v| v.to_str()) {
        Some(ext) => {
            let suffix = format!(".{ext}");
            archive_rel
                .strip_suffix(&suffix)
                .unwrap_or(archive_rel)
                .to_string()
                + ".av1.mp4"
        }
        None => format!("{archive_rel}.av1.mp4"),
    }
}

fn validate_av1_output(path: &Path) -> Result<(u64, String)> {
    let stat = fs::metadata(path).context("stat av1 archive")?;
    if stat.len() == 0 {
        bail!("av1 archive output is empty");
    }
    probe_readable(path)?;
    Ok((stat.len(), sha256_file(path)?))
}

fn probe_readable(path: &Path) -> Result<()> {
    let output = Command::new("ffprobe")
        .args(["-v", "error", "-show_streams", "-select_streams", "v:0"])
        .arg(path)
        .output()
        .context("run ffprobe")?;
    if !output.status.success() {
        bail!(
            "ffprobe av1 archive failed: {}",
            String::from_utf8_lossy(&output.stderr).trim()
        );
    }
    if output.stdout.iter().all(u8::is_ascii_whitespace) {
        bail!("av1 archive has no video stream");
    }
    Ok(())
}

fn replace_archive_with_av1(archive_path: &Path, av1_path: &Path) -> Result<()> {
    match fs::remove_file(archive_path) {
        Ok(()) => {}
        Err(err) if err.kind() == std::io::ErrorKind::NotFound => {}
        Err(err) => return Err(err).context("remove original archive"),
    }
    fs::rename(av1_path, archive_path).context("publish av1 archive")
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

fn ensure_svt_av1() -> Result<()> {
    let output = Command::new("ffmpeg")
        .args(["-hide_banner", "-encoders"])
        .output()
        .context("check ffmpeg encoders")?;
    if !output.status.success() {
        bail!(
            "check ffmpeg encoders failed: {}",
            String::from_utf8_lossy(&output.stderr).trim()
        );
    }
    if !output
        .stdout
        .windows(b"libsvtav1".len())
        .any(|w| w == b"libsvtav1")
    {
        bail!("ffmpeg libsvtav1 encoder is not available");
    }
    Ok(())
}

fn run_command(program: &str, args: &[String]) -> Result<()> {
    let output = Command::new(program)
        .args(args)
        .output()
        .with_context(|| format!("run {program}"))?;
    if !output.status.success() {
        let stderr = String::from_utf8_lossy(&output.stderr);
        let tail = tail(&stderr, 2048);
        bail!("{program} failed: {tail}");
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
            task_type: "transcode",
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

fn tail(value: &str, max: usize) -> &str {
    if value.len() <= max {
        value
    } else {
        &value[value.len() - max..]
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn artifact_path_accepts_relative_and_rejects_escape() {
        let (rel, full) = artifact_path("/records", "赛事/Round-1/主视角.mp4").unwrap();
        assert_eq!(rel, "赛事/Round-1/主视角.mp4");
        assert!(full.ends_with(Path::new("赛事/Round-1/主视角.mp4")));
        assert!(artifact_path("/records", "../主视角.mp4").is_err());
        assert!(artifact_path("/records", "/tmp/主视角.mp4").is_err());
    }

    #[test]
    fn av1_path_uses_fixed_suffix() {
        assert_eq!(
            av1_archive_path("Event/Round-1/主视角.mp4"),
            "Event/Round-1/主视角.av1.mp4"
        );
    }

    #[test]
    fn ffmpeg_args_use_config() {
        let args = transcode_ffmpeg_args(
            Path::new("in.mp4"),
            Path::new("out.av1.mp4"),
            &TranscodeConf {
                av1_preset: 6,
                av1_bitrate: "700k".to_string(),
                av1_gop: 240,
                av1_pix_fmt: "yuv420p10le".to_string(),
                ..Default::default()
            },
        );
        let joined = args.join("\0");
        for want in [
            "-preset\06",
            "-b:v\0700k",
            "-g\0240",
            "-pix_fmt\0yuv420p10le",
        ] {
            assert!(joined.contains(want), "missing {want:?} in {args:?}");
        }
    }

    #[test]
    fn replace_archive_with_av1_deletes_original_and_publishes_av1() {
        let dir = tempfile::tempdir().unwrap();
        let archive = dir.path().join("主视角.mp4");
        let av1 = dir.path().join("主视角.av1.mp4");
        fs::write(&archive, b"original").unwrap();
        fs::write(&av1, b"av1").unwrap();
        replace_archive_with_av1(&archive, &av1).unwrap();
        assert_eq!(fs::read(&archive).unwrap(), b"av1");
        assert!(!av1.exists());
    }

    #[test]
    fn validate_av1_output_rejects_empty_before_probe() {
        let dir = tempfile::tempdir().unwrap();
        let av1 = dir.path().join("主视角.av1.mp4");
        fs::write(&av1, []).unwrap();
        assert!(validate_av1_output(&av1).is_err());
    }
}
