package cn.scutbot.sim.rmdispatcher.listener.impl

import cn.scutbot.sim.rmdispatcher.data.dji.Match
import cn.scutbot.sim.rmdispatcher.listener.IMatchStartListener
import cn.scutbot.sim.rmdispatcher.service.IWebhookService
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.autoconfigure.condition.ConditionalOnProperty
import org.springframework.scheduling.annotation.Async
import org.springframework.stereotype.Component

@Component
@ConditionalOnProperty(prefix = "webhook", name = ["enabled"], havingValue = "true")
class WebhookMatchStartListener(
    @Autowired val webhookService: IWebhookService
) : IMatchStartListener {
    companion object {
        const val WEBHOOK_TYPE = "match_start"
    }

    @Async
    override fun onMatchStart(match: Match) {
        webhookService.sendWebhooks(WEBHOOK_TYPE, match)
    }
}