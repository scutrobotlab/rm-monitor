package cn.scutbot.sim.rmdispatcher.data

data class WebhookData<T>(
    val type: String,
    val data: T
)
