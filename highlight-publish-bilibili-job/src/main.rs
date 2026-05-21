use std::{
    collections::HashMap,
    fs::{self, File},
    path::{Path, PathBuf},
};

use anyhow::{bail, Context, Result};
use biliup::{
    client::Client as BiliClient,
    line::Probe,
    video::{BiliBili, Studio},
    VideoFile,
};
use bytes::Bytes;
use chrono::Utc;
use clap::Parser;
use futures::StreamExt;
use serde::{Deserialize, Serialize};
use serde_json::Value;

#[derive(Parser)]
struct Args {
    #[arg(short = 'f', default_value = "etc/config.yml")]
    config: PathBuf,
}

#[derive(Debug, Deserialize)]
struct Config {
    #[serde(rename = "RecordConf", default)]
    record: RecordConf,
    #[serde(rename = "PublishConf", default)]
    publish: PublishConf,
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
struct PublishConf {
    #[serde(rename = "Bilibili", default)]
    bilibili: BilibiliConf,
}

#[derive(Debug, Deserialize)]
struct BilibiliConf {
    #[serde(rename = "Enabled", default)]
    enabled: bool,
    #[serde(rename = "CookiePath", default = "default_cookie_path")]
    cookie_path: String,
    #[serde(rename = "TID", default = "default_tid")]
    tid: u16,
    #[serde(rename = "Copyright", default = "default_copyright")]
    copyright: u8,
    #[serde(rename = "Source", default = "default_source")]
    source: String,
    #[serde(rename = "TitleTemplate", default = "default_title_template")]
    title_template: String,
    #[serde(rename = "DescTemplate", default = "default_desc_template")]
    desc_template: String,
    #[serde(rename = "DynamicTemplate", default = "default_dynamic_template")]
    dynamic_template: String,
    #[serde(rename = "Tags", default = "default_tags")]
    tags: Vec<String>,
    #[serde(rename = "NoReprint", default)]
    no_reprint: bool,
    #[serde(rename = "OpenElec", default = "default_true")]
    open_elec: bool,
    #[serde(rename = "Cover", default)]
    cover: CoverConf,
}

impl Default for BilibiliConf {
    fn default() -> Self {
        Self {
            enabled: false,
            cookie_path: default_cookie_path(),
            tid: default_tid(),
            copyright: default_copyright(),
            source: default_source(),
            title_template: default_title_template(),
            desc_template: default_desc_template(),
            dynamic_template: default_dynamic_template(),
            tags: default_tags(),
            no_reprint: false,
            open_elec: true,
            cover: CoverConf::default(),
        }
    }
}

#[derive(Debug, Deserialize)]
struct CoverConf {
    #[serde(rename = "Enabled", default = "default_true")]
    enabled: bool,
}

impl Default for CoverConf {
    fn default() -> Self {
        Self { enabled: true }
    }
}

fn default_cookie_path() -> String {
    "/etc/biliup/cookies.json".to_string()
}
fn default_tid() -> u16 {
    232
}
fn default_copyright() -> u8 {
    2
}
fn default_source() -> String {
    "RoboMaster机甲大师".to_string()
}
fn default_title_template() -> String {
    "{{.LLMTitle}} {{.Event}}-{{.Zone}} 高光时刻".to_string()
}
fn default_desc_template() -> String {
    "{{.Description}}\n\n{{.Event}} {{.Zone}}\n{{.MatchName}} Round {{.RoundNo}}".to_string()
}
fn default_dynamic_template() -> String {
    "{{.Title}}".to_string()
}
fn default_tags() -> Vec<String> {
    vec!["RoboMaster", "机甲大师", "机器人", "赛事高光"]
        .into_iter()
        .map(str::to_string)
        .collect()
}
fn default_true() -> bool {
    true
}
#[derive(Debug, Deserialize, Serialize)]
struct PublishContext {
    task_id: i32,
    highlight_index: i32,
    start_seconds: f64,
    peak_seconds: f64,
    output_dir: String,
    llm_title: String,
    description: String,
    tags: Vec<String>,
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

#[derive(Debug, Serialize, Deserialize)]
struct HighlightJSON {
    #[serde(default)]
    title: String,
    #[serde(default)]
    description: String,
    #[serde(default)]
    tags: Vec<String>,
    #[serde(default)]
    publish: Value,
}

#[tokio::main]
async fn main() -> Result<()> {
    let args = Args::parse();
    let config: Config = serde_yaml::from_slice(
        &fs::read(&args.config)
            .with_context(|| format!("read config {}", args.config.display()))?,
    )?;
    if !config.publish.bilibili.enabled {
        bail!("bilibili publish is disabled");
    }
    let raw_context =
        std::env::var("RM_MONITOR_JOB_CONTEXT").context("RM_MONITOR_JOB_CONTEXT is required")?;
    let ctx: PublishContext =
        serde_json::from_str(&raw_context).context("parse publish context")?;
    let _ = write_context(&config, &ctx, &raw_context);
    if let Err(err) = run(&config, &ctx).await {
        let _ = write_error(&config, &ctx, &err.to_string());
        return Err(err);
    }
    Ok(())
}

async fn run(config: &Config, ctx: &PublishContext) -> Result<()> {
    let base_dir = PathBuf::from(&config.record.base_dir);
    let output_dir = resolve_under(&base_dir, &ctx.output_dir)?;
    let publish_video = output_dir.join("video-artifact.mp4");
    let cover = output_dir.join("cover.jpg");
    let publish_dir = output_dir.join("publish");
    fs::create_dir_all(&publish_dir).context("create publish dir")?;
    ensure_file(&publish_video)?;
    if config.publish.bilibili.cover.enabled {
        ensure_file(&cover)?;
    }

    let metadata = render_metadata(config, &ctx)?;
    let copied_cookie = copy_cookie(&config.publish.bilibili.cookie_path)?;
    let result = upload_bilibili(
        &config.publish.bilibili,
        &metadata,
        &publish_video,
        config
            .publish
            .bilibili
            .cover
            .enabled
            .then_some(cover.as_path()),
        &copied_cookie,
    )
    .await?;
    write_result(&output_dir, ctx.task_id, &result)?;
    update_highlight_json(output_dir.join("highlight.json"), &result)
        .context("update highlight json")?;
    Ok(())
}

#[derive(Debug)]
struct Metadata {
    title: String,
    desc: String,
    dynamic: String,
    tags: Vec<String>,
}

fn render_metadata(config: &Config, ctx: &PublishContext) -> Result<Metadata> {
    let mut data = HashMap::new();
    let match_name = format!(
        "{}. {}-{} VS {}-{}",
        ctx.order, ctx.red_school, ctx.red_name, ctx.blue_school, ctx.blue_name
    );
    let title = ctx.llm_title.trim();
    let rendered_title = if title.is_empty() {
        format!("Highlight-{:02}", ctx.highlight_index)
    } else {
        title.to_string()
    };
    data.insert("LLMTitle", rendered_title.clone());
    data.insert("Title", rendered_title);
    data.insert("Description", ctx.description.clone());
    data.insert("Event", ctx.event.clone());
    data.insert("Zone", ctx.zone.clone());
    data.insert("MatchName", match_name);
    data.insert("RoundNo", ctx.round_no.to_string());
    data.insert("MatchType", ctx.match_type.clone());
    let conf = &config.publish.bilibili;
    let mut tags = conf.tags.clone();
    for tag in &ctx.tags {
        if !tag.trim().is_empty() && !tags.iter().any(|v| v == tag) {
            tags.push(tag.clone());
        }
    }
    Ok(Metadata {
        title: render_template(&conf.title_template, &data)
            .chars()
            .take(80)
            .collect(),
        desc: render_template(&conf.desc_template, &data),
        dynamic: render_template(&conf.dynamic_template, &data)
            .chars()
            .take(233)
            .collect(),
        tags,
    })
}

fn render_template(tpl: &str, data: &HashMap<&str, String>) -> String {
    let mut out = tpl.to_string();
    for (k, v) in data {
        out = out.replace(&format!("{{{{.{k}}}}}"), v);
        out = out.replace(&format!("{{{k}}}"), v);
    }
    out
}

#[derive(Debug, Serialize)]
struct PublishResult {
    external_id: Option<String>,
    url: Option<String>,
    raw: Value,
}

async fn upload_bilibili(
    conf: &BilibiliConf,
    meta: &Metadata,
    video: &Path,
    cover: Option<&Path>,
    cookie: &Path,
) -> Result<PublishResult> {
    let client = BiliClient::new();
    let login_info = client
        .login_by_cookies(File::options().read(true).write(true).open(cookie)?)
        .await
        .context("login biliup cookies")?;
    let video_file = VideoFile::new(video).context("open upload video")?;
    let line = Probe::probe().await.context("probe upload line")?;
    let uploader = line.to_uploader(video_file);
    let part = uploader
        .upload(&client, 3, |stream| {
            stream
                .map(|chunk| {
                    chunk
                        .map(|bytes: Bytes| {
                            let len = bytes.len();
                            (bytes, len)
                        })
                        .map_err(Into::into)
                })
                .boxed()
        })
        .await
        .context("upload video")?;
    let cover_url = match cover {
        Some(path) => {
            let image = fs::read(path).with_context(|| format!("read cover {}", path.display()))?;
            Some(
                BiliBili::new(&login_info, &client)
                    .cover_up(&image)
                    .await
                    .context("upload cover")?,
            )
        }
        None => None,
    };
    let tag = meta.tags.join(",");
    let mut studio = Studio::builder()
        .title(meta.title.clone())
        .desc(meta.desc.clone())
        .dynamic(meta.dynamic.clone())
        .tag(tag)
        .tid(conf.tid)
        .copyright(conf.copyright)
        .source(conf.source.clone())
        .videos(vec![part])
        .build();
    if let Some(cover_url) = cover_url {
        studio.cover = cover_url;
    }
    studio.no_reprint = Some(conf.no_reprint as u8);
    studio.open_elec = Some(conf.open_elec as u8);
    let raw_value = studio
        .submit(&login_info)
        .await
        .context("submit biliup studio")?;
    let external_id = first_string(&raw_value, &["bvid", "aid"]);
    let url = external_id.as_ref().map(|id| {
        if id.starts_with("BV") {
            format!("https://www.bilibili.com/video/{id}")
        } else {
            format!("https://www.bilibili.com/video/av{id}")
        }
    });
    Ok(PublishResult {
        external_id,
        url,
        raw: raw_value,
    })
}

fn first_string(v: &Value, keys: &[&str]) -> Option<String> {
    match v {
        Value::Object(map) => {
            for key in keys {
                if let Some(value) = map.get(*key) {
                    if let Some(s) = value.as_str() {
                        return Some(s.to_string());
                    }
                    if let Some(n) = value.as_i64() {
                        return Some(n.to_string());
                    }
                }
            }
            map.values().find_map(|v| first_string(v, keys))
        }
        Value::Array(values) => values.iter().find_map(|v| first_string(v, keys)),
        _ => None,
    }
}

fn update_highlight_json(path: PathBuf, result: &PublishResult) -> Result<()> {
    let mut meta: HighlightJSON = serde_json::from_slice(
        &fs::read(&path).with_context(|| format!("read {}", path.display()))?,
    )?;
    let mut publish = match meta.publish {
        Value::Object(map) => map,
        _ => serde_json::Map::new(),
    };
    publish.insert(
        "bilibili".to_string(),
        serde_json::json!({
            "status": "succeeded",
            "url": result.url,
            "external_id": result.external_id,
            "raw": result.raw,
            "completed_at": Utc::now(),
        }),
    );
    meta.publish = Value::Object(publish);
    let raw = serde_json::to_vec_pretty(&meta)?;
    atomic_write(&path, &raw)
}

fn write_result(output_dir: &Path, task_id: i32, result: &PublishResult) -> Result<()> {
    let raw = serde_json::to_vec_pretty(result)?;
    atomic_write(&job_dir(output_dir, task_id).join("result.json"), &raw)
}

fn write_error(config: &Config, ctx: &PublishContext, msg: &str) -> Result<()> {
    let output_dir = resolve_under(Path::new(&config.record.base_dir), &ctx.output_dir)?;
    let raw = serde_json::to_vec_pretty(&serde_json::json!({
        "status": "failed",
        "task_type": "highlight-publish-bilibili",
        "task_id": ctx.task_id,
        "error_message": msg.chars().take(2000).collect::<String>(),
        "completed_at": Utc::now(),
    }))?;
    atomic_write(&job_dir(&output_dir, ctx.task_id).join("error.json"), &raw)
}

fn write_context(config: &Config, ctx: &PublishContext, raw_context: &str) -> Result<()> {
    let output_dir = resolve_under(Path::new(&config.record.base_dir), &ctx.output_dir)?;
    let parsed: Value = serde_json::from_str(raw_context)?;
    atomic_write(
        &job_dir(&output_dir, ctx.task_id).join("context.json"),
        &serde_json::to_vec_pretty(&parsed)?,
    )
}

fn job_dir(output_dir: &Path, task_id: i32) -> PathBuf {
    output_dir
        .join(".job")
        .join(format!("highlight-publish-bilibili-{task_id}"))
}

fn resolve_under(base: &Path, rel: &str) -> Result<PathBuf> {
    let clean = rel.trim().trim_start_matches('/');
    if clean.contains("..") {
        bail!("path escapes base dir: {rel}");
    }
    Ok(base.join(clean))
}

fn ensure_file(path: &Path) -> Result<()> {
    let meta = fs::metadata(path).with_context(|| format!("stat {}", path.display()))?;
    if !meta.is_file() || meta.len() == 0 {
        bail!("file is missing or empty: {}", path.display());
    }
    Ok(())
}

fn atomic_write(path: &Path, data: &[u8]) -> Result<()> {
    if let Some(parent) = path.parent() {
        fs::create_dir_all(parent)?;
    }
    let tmp = path.with_extension("tmp");
    fs::write(&tmp, data)?;
    fs::rename(tmp, path)?;
    Ok(())
}

fn copy_cookie(cookie_path: &str) -> Result<PathBuf> {
    let source = PathBuf::from(cookie_path);
    ensure_file(&source)?;
    let target = std::env::temp_dir().join("biliup-cookies.json");
    fs::copy(&source, &target).with_context(|| format!("copy cookie {}", source.display()))?;
    Ok(target)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn renders_templates() {
        let conf = Config {
            record: RecordConf {
                base_dir: "/records".into(),
            },
            publish: PublishConf {
                bilibili: BilibiliConf::default(),
            },
        };
        let ctx = PublishContext {
            task_id: 1,
            highlight_index: 3,
            start_seconds: 10.0,
            peak_seconds: 20.0,
            output_dir: "A/B".into(),
            llm_title: "精彩团战".into(),
            description: "描述".into(),
            tags: vec!["高光".into()],
            event: "RMUC".into(),
            zone: "南部赛区".into(),
            order: 55,
            match_type: "BO5".into(),
            round_no: 1,
            red_school: "红校".into(),
            red_name: "红队".into(),
            blue_school: "蓝校".into(),
            blue_name: "蓝队".into(),
        };
        let meta = render_metadata(&conf, &ctx).unwrap();
        assert_eq!(meta.title, "精彩团战 RMUC-南部赛区 高光时刻");
        assert!(meta.desc.contains("RMUC 南部赛区"));
        assert!(meta.tags.contains(&"RoboMaster".to_string()));
        assert!(meta.tags.contains(&"高光".to_string()));
    }

    #[test]
    fn extracts_bvid() {
        let raw = serde_json::json!({"data": {"bvid": "BV123"}});
        assert_eq!(
            first_string(&raw, &["bvid", "aid"]).as_deref(),
            Some("BV123")
        );
    }
}
