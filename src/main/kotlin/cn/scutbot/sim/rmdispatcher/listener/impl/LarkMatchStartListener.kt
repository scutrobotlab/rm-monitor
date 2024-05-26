package cn.scutbot.sim.rmdispatcher.listener.impl

import cn.scutbot.sim.rmdispatcher.data.dji.Match
import cn.scutbot.sim.rmdispatcher.data.lark.CardVariable
import cn.scutbot.sim.rmdispatcher.listener.IMatchStartListener
import cn.scutbot.sim.rmdispatcher.service.ILarkService
import cn.scutbot.sim.rmdispatcher.service.IMatchService
import cn.scutbot.sim.rmdispatcher.utils.logger
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.beans.factory.annotation.Value
import org.springframework.boot.autoconfigure.condition.ConditionalOnProperty
import org.springframework.scheduling.annotation.Async
import org.springframework.stereotype.Component

@Component
@ConditionalOnProperty(prefix = "lark", name = ["enabled"], havingValue = "true")
class LarkMatchStartListener(
    @Autowired val larkService: ILarkService,
    @Autowired val matchService: IMatchService,
    @Value("\${lark.cardId}") val cardId: String,
) : IMatchStartListener {

    @Async
    override fun onMatchStart(match: Match) {
        logger().info("Match ${match.id ?: return} started")

        val vars = CardVariable(match)

        var messageIds = matchService.getMessageIds(match.id)

        if (messageIds.isEmpty()) {
            match.slug?.takeIf { it.isNotBlank() }?.let {
                vars.matchType = it
            }

            larkService.uploadImg(match.redSide?.player?.team?.collegeLogo)?.let {
                vars.redAvatar = it
            }

            larkService.uploadImg(match.blueSide?.player?.team?.collegeLogo)?.let {
                vars.blueAvatar = it
            }

            messageIds = larkService.notifyCard(cardId, vars, larkService.joinedGroups(), larkService.webhooks())
        }

        matchService.saveMatchCardVars(match.id, vars)
        matchService.saveMessageIds(match.id, messageIds)
    }
}
