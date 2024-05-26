package cn.scutbot.sim.rmdispatcher.service

import cn.scutbot.sim.rmdispatcher.data.lark.newTemplateMessageCard
import com.lark.oapi.service.im.v1.enums.CreateMessageReceiveIdTypeEnum
import java.nio.file.Path
import java.util.concurrent.CompletableFuture

interface ILarkService {
    fun sendMessage(
        receiverIdType: CreateMessageReceiveIdTypeEnum,
        receiverId: String,
        msgType: String,
        content: String
    ): CompletableFuture<String>

    fun <T> sendWebhookCard(
        url: String,
        cardId: String,
        vars: T
    )

    fun joinedGroups(): Set<String>

    fun webhooks(): Set<String>

    fun <T> notifyCard(cardId: String, vars: T, groups: Set<String>, webhooks: Set<String>) : Set<String> {
        val card = newTemplateMessageCard(cardId, vars)

        val futures = groups.map {
            sendMessage(CreateMessageReceiveIdTypeEnum.CHAT_ID, it, "interactive", card)
        }

        webhooks.forEach {
            sendWebhookCard(it, cardId, vars)
        }

        val messageIds = futures.map { it.get() }

        return messageIds.toSet()
    }

    fun <T> updateNotifyCard(messageIds: Set<String>, cardId: String, vars: T)

    fun uploadImg(
        sourceUrl: String?
    ): String?

    fun uploadImg(
        file: Path
    ): String?
}
