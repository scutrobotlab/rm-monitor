package cn.scutbot.sim.rmdispatcher.listener.impl

import cn.scutbot.sim.rmdispatcher.data.dji.Match
import cn.scutbot.sim.rmdispatcher.listener.IMatchEndListener
import cn.scutbot.sim.rmdispatcher.service.IWebhookService
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.autoconfigure.condition.ConditionalOnProperty
import org.springframework.stereotype.Component

@Component
@ConditionalOnProperty(prefix = "webhook", name = ["enabled"], havingValue = "true")
class WebhookMatchEndListener(
    @Autowired val webhookService: IWebhookService
) : IMatchEndListener {
    companion object {
        const val WEBHOOK_TYPE = "match_end"
    }

    override fun onMatchEnd(match: Match) {
        webhookService.sendWebhooks(WEBHOOK_TYPE, match)
    }
}