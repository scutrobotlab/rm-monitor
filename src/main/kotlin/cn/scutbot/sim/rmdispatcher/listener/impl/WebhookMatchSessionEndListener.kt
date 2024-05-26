package cn.scutbot.sim.rmdispatcher.listener.impl

import cn.scutbot.sim.rmdispatcher.data.dji.Match
import cn.scutbot.sim.rmdispatcher.listener.IMatchSessionEndListener
import cn.scutbot.sim.rmdispatcher.service.IWebhookService
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.autoconfigure.condition.ConditionalOnProperty
import org.springframework.stereotype.Component

@Component
@ConditionalOnProperty(prefix = "webhook", name = ["enabled"], havingValue = "true")
class WebhookMatchSessionEndListener(
    @Autowired val webhookService: IWebhookService
) : IMatchSessionEndListener {
    companion object {
        const val WEBHOOK_TYPE = "match_session_end"
    }

    override fun onMatchSessionEnd(match: Match) {
        webhookService.sendWebhooks(WEBHOOK_TYPE, match)
    }
}