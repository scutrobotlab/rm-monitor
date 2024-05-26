package cn.scutbot.sim.rmdispatcher

import cn.scutbot.sim.rmdispatcher.data.dji.CurrentAndNextMatch
import cn.scutbot.sim.rmdispatcher.data.dji.DjiFilterContext
import cn.scutbot.sim.rmdispatcher.data.dji.Match
import cn.scutbot.sim.rmdispatcher.filter.IDjiInfoFilter
import cn.scutbot.sim.rmdispatcher.service.IDjiService
import cn.scutbot.sim.rmdispatcher.utils.logger
import org.springframework.beans.factory.annotation.Value
import org.springframework.scheduling.annotation.Async
import org.springframework.scheduling.annotation.Scheduled
import org.springframework.stereotype.Component

@Component
class RecorderDispatcher(
    val djiService: IDjiService,
    val filters: List<IDjiInfoFilter>,

    @Value("\${dji.apiUrl}") val djiApi: String
) {
    @Async
    @Scheduled(fixedRateString = "\${dji.scanRate}")
    fun asyncScan() {
        logger().debug("Starting DJI match scan on api $djiApi")
        val info = djiService.fetchInfo()

        info.parallelStream().forEach { match ->
            val initContext = DjiFilterContext(match.currentMatch ?: Match.EMPTY)
            initContext.meta["key"] = match.extractUid()

            var context = initContext
            for (filter in filters) {
                try {
                    if (context.hasNext && filter.condition(context)) {
                        context = filter.filter(context)
                    }
                } catch (e: Exception) {
                    logger().error("Error in filter ${filter.javaClass.simpleName}", e)
                }
            }
        }
    }

    @Async
    @Scheduled(fixedRate = 600000)
    fun asyncCleanUp() {
        logger().debug("Starting DJI match cleanup")
        val info = djiService.fetchInfo()
        val availableZones = info.stream().filter {
            it.currentMatch?.zone?.name != null
        }.map {
            it.currentMatch!!.zone!!.name!!
        }.toList()

        val toClean = djiService.zones().minus(availableZones.toSet())
        logger().info("Cleaning up dataset in $toClean")
        toClean.forEach {
            val initContext = DjiFilterContext(Match.EMPTY)
            initContext.meta["key"] = "$key-$it"

            var context = initContext
            for (filter in filters) {
                if (context.hasNext && filter.condition(context)) {
                    context = filter.filter(context)
                }
            }
        }
    }

    val key: String = "RM-Dispatcher"
    fun CurrentAndNextMatch.extractUid(): String {
        return if (currentMatch?.zone?.name != null)
            "$key-${currentMatch.zone?.name}"
        else if (nextMatch?.zone?.name != null)
            "$key-${nextMatch.zone?.name}"
        else
            key
    }
}
