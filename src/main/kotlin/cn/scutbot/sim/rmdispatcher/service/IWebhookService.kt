package cn.scutbot.sim.rmdispatcher.service

interface IWebhookService {
    fun getWebhookEndpoints(): Set<String>

    fun <T> sendWebhook(endpoint: String, type: String, payload: T)

    fun <T> sendWebhooks(type: String, payload: T) {
        getWebhookEndpoints().forEach {
            sendWebhook(it, type, payload)
        }
    }
}