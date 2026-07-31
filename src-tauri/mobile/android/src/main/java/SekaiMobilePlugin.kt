package com.sekaitext.mobile

import android.app.Activity
import android.content.Intent
import android.net.Uri
import app.tauri.annotation.Command
import app.tauri.annotation.InvokeArg
import app.tauri.annotation.TauriPlugin
import app.tauri.plugin.JSObject
import app.tauri.plugin.Invoke
import app.tauri.plugin.Plugin
import com.sekaitext.mobilecore.mobilecore.Mobilecore
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.json.JSONObject

@InvokeArg
class JsonRequest {
    var requestJson: String? = null
}

@InvokeArg
class ContentRequest {
    var content: String? = null
}

@TauriPlugin
class SekaiMobilePlugin(private val activity: Activity) : Plugin(activity) {
    // Tauri schedules Android plugin commands on the main thread. All gomobile,
    // disk, and network work therefore runs on Dispatchers.IO to avoid UI freezes
    // and Android ANRs. Initialization is also asynchronous and each subsystem's
    // commands wait only for the initialization they actually depend on.
    private val ioScope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private val storyInitialization = CompletableDeferred<String?>()
    private val glossaryInitialization = CompletableDeferred<String?>()
    private val teamInitialization = CompletableDeferred<String?>()
    private val live2dInitialization = CompletableDeferred<String?>()

    init {
        // gomobile uses this context for Java callbacks and Android-bound packages.
        go.Seq.setContext(activity.applicationContext)

        ioScope.launch {
            storyInitialization.complete(initializationError {
                Mobilecore.initialize(
                    activity.noBackupFilesDir.resolve("sekaitext/story").absolutePath,
                    activity.cacheDir.resolve("sekaitext/story").absolutePath,
                )
            })
        }
        ioScope.launch {
            glossaryInitialization.complete(initializationError {
                Mobilecore.initializeGlossary(
                    activity.noBackupFilesDir.resolve("sekaitext/glossary").absolutePath,
                )
            })
        }
        ioScope.launch {
            val glossaryError = glossaryInitialization.await()
            teamInitialization.complete(
                glossaryError?.let { "glossary initialization failed: $it" }
                    ?: initializationError {
                        Mobilecore.initializeTeam(
                            activity.noBackupFilesDir.resolve("sekaitext/glossary").absolutePath,
                        )
                    },
            )
        }
        ioScope.launch {
            live2dInitialization.complete(initializationError {
                Mobilecore.initializeLive2DAssetCache(
                    activity.cacheDir.resolve("sekaitext/live2d").absolutePath,
                )
            })
        }
    }

    @Command
    fun bootstrap(invoke: Invoke) {
        run(invoke) {
            val storyError = storyInitialization.await()
            val glossaryError = glossaryInitialization.await()
            val teamError = teamInitialization.await()
            val live2dError = live2dInitialization.await()
            JSONObject(Mobilecore.bootstrap())
                .put("story", storyError == null)
                .put("glossary", glossaryError == null)
                .put("team", teamError == null)
                .put("live2d", live2dError == null)
                .toString()
        }
    }

    @Command
    fun openUrl(invoke: Invoke) {
        run(invoke) {
            val args = invoke.parseArgs(JsonRequest::class.java)
            val rawUrl = JSONObject(args.requestJson ?: "{}").optString("url").trim()
            val uri = Uri.parse(rawUrl)
            val scheme = uri.scheme?.lowercase()
            if (scheme !in setOf("http", "https") || uri.host.isNullOrBlank() || uri.userInfo != null) {
                throw IllegalArgumentException("only absolute http/https URLs without credentials can be opened")
            }
            withContext(Dispatchers.Main) {
                val intent = Intent(Intent.ACTION_VIEW, uri).addCategory(Intent.CATEGORY_BROWSABLE)
                activity.startActivity(intent)
            }
            JSONObject().put("status", "opened").toString()
        }
    }

    @Command
    fun storyTypes(invoke: Invoke) {
        runStory(invoke) { Mobilecore.storyTypes() }
    }

    @Command
    fun storySorts(invoke: Invoke) {
        runStoryJsonRequest(invoke, Mobilecore::storySorts)
    }

    @Command
    fun storyIndex(invoke: Invoke) {
        runStoryJsonRequest(invoke, Mobilecore::storyIndex)
    }

    @Command
    fun storyChapters(invoke: Invoke) {
        runStoryJsonRequest(invoke, Mobilecore::storyChapters)
    }

    @Command
    fun storyJsonPath(invoke: Invoke) {
        runStoryJsonRequest(invoke, Mobilecore::storyJSONPath)
    }

    @Command
    fun storyLoad(invoke: Invoke) {
        runStoryJsonRequest(invoke, Mobilecore::storyLoad)
    }

    @Command
    fun storyLoadLocal(invoke: Invoke) {
        runStory(invoke) {
            val args = invoke.parseArgs(ContentRequest::class.java)
            Mobilecore.storyLoadLocal(args.content ?: "")
        }
    }

    @Command
    fun voiceUrl(invoke: Invoke) {
        runStoryJsonRequest(invoke, Mobilecore::voiceURL)
    }

    @Command
    fun resolveLive2DAsset(invoke: Invoke) {
        runLive2DJsonRequest(invoke, Mobilecore::resolveLive2DAsset)
    }

    @Command
    fun resolveStoryLabel(invoke: Invoke) {
        runStoryJsonRequest(invoke, Mobilecore::resolveStoryLabel)
    }

    @Command
    fun storyCatalogStatus(invoke: Invoke) {
        runStory(invoke) { Mobilecore.storyCatalogStatus() }
    }

    @Command
    fun updateStoryCatalog(invoke: Invoke) {
        runStory(invoke) { Mobilecore.updateStoryCatalog() }
    }

    @Command
    fun storyUpdateProgress(invoke: Invoke) {
        runStory(invoke) { Mobilecore.storyUpdateProgress() }
    }

    @Command
    fun createTranslation(invoke: Invoke) {
        run(invoke) {
            val args = invoke.parseArgs(JsonRequest::class.java)
            Mobilecore.createTranslation(args.requestJson ?: "{}")
        }
    }

    @Command
    fun loadTranslation(invoke: Invoke) {
        run(invoke) {
            val args = invoke.parseArgs(ContentRequest::class.java)
            Mobilecore.loadTranslation(args.content ?: "")
        }
    }

    @Command
    fun serializeTranslation(invoke: Invoke) {
        run(invoke) {
            val args = invoke.parseArgs(JsonRequest::class.java)
            Mobilecore.serializeTranslation(args.requestJson ?: "{}")
        }
    }

    @Command
    fun checkLines(invoke: Invoke) {
        runJsonRequest(invoke, Mobilecore::checkLines)
    }

    @Command
    fun compareText(invoke: Invoke) {
        runJsonRequest(invoke, Mobilecore::compareText)
    }

    @Command
    fun changeText(invoke: Invoke) {
        runJsonRequest(invoke, Mobilecore::changeText)
    }

    @Command
    fun addLine(invoke: Invoke) {
        runJsonRequest(invoke, Mobilecore::addLine)
    }

    @Command
    fun removeLine(invoke: Invoke) {
        runJsonRequest(invoke, Mobilecore::removeLine)
    }

    @Command
    fun replaceBrackets(invoke: Invoke) {
        runJsonRequest(invoke, Mobilecore::replaceBrackets)
    }

    @Command
    fun speakerCount(invoke: Invoke) {
        runJsonRequest(invoke, Mobilecore::speakerCount)
    }

    @Command
    fun checkText(invoke: Invoke) {
        runJsonRequest(invoke, Mobilecore::checkText)
    }

    @Command
    fun glossarySearch(invoke: Invoke) {
        runGlossaryJsonRequest(invoke, Mobilecore::glossarySearch)
    }

    @Command
    fun glossaryCategories(invoke: Invoke) {
        runGlossary(invoke) { Mobilecore.glossaryCategories() }
    }

    @Command
    fun glossaryEntries(invoke: Invoke) {
        runGlossaryJsonRequest(invoke, Mobilecore::glossaryEntries)
    }

    @Command
    fun glossaryAddEntry(invoke: Invoke) {
        runGlossaryJsonRequest(invoke, Mobilecore::glossaryAddEntry)
    }

    @Command
    fun glossaryUpdateEntry(invoke: Invoke) {
        runGlossaryJsonRequest(invoke, Mobilecore::glossaryUpdateEntry)
    }

    @Command
    fun glossaryDeleteEntry(invoke: Invoke) {
        runGlossaryJsonRequest(invoke, Mobilecore::glossaryDeleteEntry)
    }

    @Command
    fun glossaryAppellationSpeakers(invoke: Invoke) {
        runGlossary(invoke) { Mobilecore.glossaryAppellationSpeakers() }
    }

    @Command
    fun glossaryAppellationTargets(invoke: Invoke) {
        runGlossaryJsonRequest(invoke, Mobilecore::glossaryAppellationTargets)
    }

    @Command
    fun glossaryAppellationLookup(invoke: Invoke) {
        runGlossaryJsonRequest(invoke, Mobilecore::glossaryAppellationLookup)
    }

    @Command
    fun glossaryAppellationUpsert(invoke: Invoke) {
        runGlossaryJsonRequest(invoke, Mobilecore::glossaryAppellationUpsert)
    }

    @Command
    fun glossaryGrammar(invoke: Invoke) {
        runGlossaryJsonRequest(invoke, Mobilecore::glossaryGrammar)
    }

    @Command
    fun glossaryExport(invoke: Invoke) {
        runGlossary(invoke) { Mobilecore.glossaryExport() }
    }

    @Command
    fun teamStatus(invoke: Invoke) {
        runTeamJsonRequest(invoke, Mobilecore::teamStatus)
    }

    @Command
    fun teamLogin(invoke: Invoke) {
        runTeamJsonRequest(invoke, Mobilecore::teamLogin)
    }

    @Command
    fun teamLogout(invoke: Invoke) {
        runTeamJsonRequest(invoke, Mobilecore::teamLogout)
    }

    @Command
    fun teamConnect(invoke: Invoke) {
        runTeamJsonRequest(invoke, Mobilecore::teamConnect)
    }

    @Command
    fun teamDisconnect(invoke: Invoke) {
        runTeamJsonRequest(invoke, Mobilecore::teamDisconnect)
    }

    @Command
    fun teamSync(invoke: Invoke) {
        runTeamJsonRequest(invoke, Mobilecore::teamSync)
    }

    @Command
    fun teamCreateProposal(invoke: Invoke) {
        runTeamJsonRequest(invoke, Mobilecore::teamCreateProposal)
    }

    @Command
    fun teamMyProposals(invoke: Invoke) {
        runTeamJsonRequest(invoke, Mobilecore::teamMyProposals)
    }

    @Command
    fun teamWithdrawProposal(invoke: Invoke) {
        runTeamJsonRequest(invoke, Mobilecore::teamWithdrawProposal)
    }

    @Command
    fun teamPendingProposals(invoke: Invoke) {
        runTeamJsonRequest(invoke, Mobilecore::teamPendingProposals)
    }

    @Command
    fun teamApproveProposal(invoke: Invoke) {
        runTeamJsonRequest(invoke, Mobilecore::teamApproveProposal)
    }

    @Command
    fun teamRejectProposal(invoke: Invoke) {
        runTeamJsonRequest(invoke, Mobilecore::teamRejectProposal)
    }

    @Command
    fun teamSetReviewer(invoke: Invoke) {
        runTeamJsonRequest(invoke, Mobilecore::teamSetReviewer)
    }

    @Command
    fun teamListUsers(invoke: Invoke) {
        runTeamJsonRequest(invoke, Mobilecore::teamListUsers)
    }

    @Command
    fun teamChangePassword(invoke: Invoke) {
        runTeamJsonRequest(invoke, Mobilecore::teamChangePassword)
    }

    @Command
    fun teamUpdateProfile(invoke: Invoke) {
        runTeamJsonRequest(invoke, Mobilecore::teamUpdateProfile)
    }

    @Command
    fun teamAccountUsers(invoke: Invoke) {
        runTeamJsonRequest(invoke, Mobilecore::teamAccountUsers)
    }

    @Command
    fun teamCreateUser(invoke: Invoke) {
        runTeamJsonRequest(invoke, Mobilecore::teamCreateUser)
    }

    @Command
    fun teamSetUserRole(invoke: Invoke) {
        runTeamJsonRequest(invoke, Mobilecore::teamSetUserRole)
    }

    @Command
    fun teamSetUserStatus(invoke: Invoke) {
        runTeamJsonRequest(invoke, Mobilecore::teamSetUserStatus)
    }

    @Command
    fun teamResetUserPassword(invoke: Invoke) {
        runTeamJsonRequest(invoke, Mobilecore::teamResetUserPassword)
    }

    @Command
    fun teamDeleteUser(invoke: Invoke) {
        runTeamJsonRequest(invoke, Mobilecore::teamDeleteUser)
    }

    @Command
    fun teamGlossaryReplace(invoke: Invoke) {
        runTeamJsonRequest(invoke, Mobilecore::teamGlossaryReplace)
    }

    @Command
    fun teamBulkImport(invoke: Invoke) {
        runTeamJsonRequest(invoke, Mobilecore::teamBulkImport)
    }

    private fun runJsonRequest(invoke: Invoke, operation: (String) -> String) {
        run(invoke) {
            val args = invoke.parseArgs(JsonRequest::class.java)
            operation(args.requestJson ?: "{}")
        }
    }

    private fun runStoryJsonRequest(invoke: Invoke, operation: (String) -> String) {
        runStory(invoke) {
            val args = invoke.parseArgs(JsonRequest::class.java)
            operation(args.requestJson ?: "{}")
        }
    }

    private fun runLive2DJsonRequest(invoke: Invoke, operation: (String) -> String) {
        run(invoke) {
            awaitInitialization(live2dInitialization, "live2d cache")
            val args = invoke.parseArgs(JsonRequest::class.java)
            operation(args.requestJson ?: "{}")
        }
    }

    private fun runGlossaryJsonRequest(invoke: Invoke, operation: (String) -> String) {
        runGlossary(invoke) {
            val args = invoke.parseArgs(JsonRequest::class.java)
            operation(args.requestJson ?: "{}")
        }
    }

    private fun runTeamJsonRequest(invoke: Invoke, operation: (String) -> String) {
        run(invoke) {
            awaitInitialization(teamInitialization, "team")
            val args = invoke.parseArgs(JsonRequest::class.java)
            operation(args.requestJson ?: "{}")
        }
    }

    private fun runGlossary(invoke: Invoke, operation: () -> String) {
        run(invoke) {
            awaitInitialization(glossaryInitialization, "glossary")
            operation()
        }
    }

    private fun runStory(invoke: Invoke, operation: () -> String) {
        run(invoke) {
            awaitInitialization(storyInitialization, "story core")
            operation()
        }
    }

    private fun run(invoke: Invoke, operation: suspend () -> String) {
        ioScope.launch {
            try {
                resolveJson(invoke, operation())
            } catch (error: Exception) {
                invoke.reject(error.message ?: error.toString())
            }
        }
    }

    private suspend fun awaitInitialization(initialization: CompletableDeferred<String?>, label: String) {
        initialization.await()?.let { throw IllegalStateException("$label initialization failed: $it") }
    }

    private inline fun initializationError(operation: () -> Unit): String? = try {
        operation()
        null
    } catch (error: Exception) {
        error.message ?: error.toString()
    }

    private fun resolveJson(invoke: Invoke, value: String) {
        val response = JSObject()
        response.put("json", value)
        invoke.resolve(response)
    }
}
