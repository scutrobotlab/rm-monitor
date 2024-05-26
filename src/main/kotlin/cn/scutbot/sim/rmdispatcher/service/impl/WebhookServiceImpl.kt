package cn.scutbot.sim.rmdispatcher.service.impl

import cn.scutbot.sim.rmdispatcher.config.WebhookConfig
import cn.scutbot.sim.rmdispatcher.data.WebhookData
import cn.scutbot.sim.rmdispatcher.service.IWebhookService
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.web.client.RestTemplateBuilder
import org.springframework.scheduling.annotation.Async
import org.springframework.stereotype.Service
import org.springframework.web.client.RestTemplate

@Service
class WebhookServiceImpl(
    @Autowired val webhookConfig: WebhookConfig,
    @Autowired val templateBuilder: RestTemplateBuilder
) : IWebhookService {
    override fun getWebhookEndpoints(): Set<String> = webhookConfig.endpoints
    val restTemplate: RestTemplate by lazy {
        templateBuilder.build()
    }

    @Async
    override fun <T> sendWebhook(endpoint: String, type: String, payload: T) {
        val data = WebhookData(type, payload)
        restTemplate.postForLocation(endpoint, data)
    }
}