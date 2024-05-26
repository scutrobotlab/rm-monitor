package cn.scutbot.sim.rmdispatcher.listener.impl

import cn.scutbot.sim.rmdispatcher.data.dji.Match
import cn.scutbot.sim.rmdispatcher.data.lark.CardVariable
import cn.scutbot.sim.rmdispatcher.data.lark.Score
import cn.scutbot.sim.rmdispatcher.listener.IMatchSessionEndListener
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
class LarkMatchSessionEndListener(
    @Autowired val larkService: ILarkService,
    @Autowired val matchService: IMatchService,
    @Value("\${lark.cardId}") val cardId: String,
): IMatchSessionEndListener {

    @Async
    override fun onMatchSessionEnd(match: Match) {
        logger().info("Match ${match.id ?: return} session ended")

        val vars = matchService.getMatchCardVars(match.id) ?: CardVariable(match)
        val messageIds = matchService.getMessageIds(match.id)

        vars.scores = vars.scores ?: emptySet()
        vars.scores = vars.scores!! + Score(
            redScore = match.redSideWinGameCount?.toString() ?: return,
            blueScore = match.blueSideWinGameCount?.toString() ?: return
        )

        matchService.saveMatchCardVars(match.id, vars)

        larkService.updateNotifyCard(messageIds.toSet(), cardId, vars)
    }
}
