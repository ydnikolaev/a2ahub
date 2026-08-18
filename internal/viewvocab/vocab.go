// Package viewvocab is the closed, bilingual (RU/EN) dashboard vocabulary —
// a pure data table plus its family/tone constants, shared by any caller that
// needs the same words the dashboard shows (a chat notifier, the CI plane)
// without importing the presentation package to get them. It imports
// nothing: every fact here is a literal, and it holds no state.
package viewvocab

// VocabularyTable is the complete payload-local dictionary for status-bearing
// values. Unknown is presentation fallback, not a fabricated domain entry.
type VocabularyTable struct {
	Entries []VocabularyEntry  `json:"entries"`
	Unknown VocabularyFallback `json:"unknown"`
}

// VocabularyEntry binds one typed family/value pair to permanent bilingual
// meaning and to the closed presentation cues the browser may apply.
type VocabularyEntry struct {
	Value         string           `json:"value"`
	Family        VocabularyFamily `json:"family"`
	LabelRU       string           `json:"labelRU"`
	LabelEN       string           `json:"labelEN"`
	ExplanationRU string           `json:"explanationRU"`
	ExplanationEN string           `json:"explanationEN"`
	Tone          VocabularyTone   `json:"tone"`
	Cue           string           `json:"cue"`
}

// VocabularyFallback is the honest result for an unrecognized family/value.
// It deliberately carries neither field, so it cannot pose as catalogue data.
type VocabularyFallback struct {
	LabelRU       string         `json:"labelRU"`
	LabelEN       string         `json:"labelEN"`
	ExplanationRU string         `json:"explanationRU"`
	ExplanationEN string         `json:"explanationEN"`
	Tone          VocabularyTone `json:"tone"`
	Cue           string         `json:"cue"`
}

// VocabularyFamily identifies one closed family in the dashboard's bilingual
// vocabulary.
type VocabularyFamily string

// VocabularyTone identifies the presentation intent and non-color cue assigned
// to a dashboard vocabulary entry.
type VocabularyTone string

// VocabularyFamily constants enumerate the closed semantic families emitted by
// the dashboard vocabulary.
const (
	VocabularyFamilyFreshness           VocabularyFamily = "freshness"
	VocabularyFamilySourceFreshness     VocabularyFamily = "source-freshness"
	VocabularyFamilyOutcome             VocabularyFamily = "outcome"
	VocabularyFamilyLifecycleState      VocabularyFamily = "lifecycle-state"
	VocabularyFamilyTransition          VocabularyFamily = "transition"
	VocabularyFamilyReason              VocabularyFamily = "reason"
	VocabularyFamilyGate                VocabularyFamily = "gate"
	VocabularyFamilyWorkMode            VocabularyFamily = "work-mode"
	VocabularyFamilyDependencyDrift     VocabularyFamily = "dependency-drift"
	VocabularyFamilyConsistencySeverity VocabularyFamily = "consistency-severity"
	VocabularyFamilyOperationalState    VocabularyFamily = "operational-state"
	VocabularyFamilyLiveTransport       VocabularyFamily = "live-transport"
)

// VocabularyTone constants enumerate the closed presentation intents available
// to dashboard vocabulary entries.
const (
	VocabularyToneNeedsYou    VocabularyTone = "needs-you"
	VocabularyToneWaitingThem VocabularyTone = "waiting-them"
	VocabularyToneProgressing VocabularyTone = "progressing"
	VocabularyToneSettled     VocabularyTone = "settled"
	VocabularyToneBroken      VocabularyTone = "broken"
	VocabularyToneUnknown     VocabularyTone = "unknown"
)

var toneCues = map[VocabularyTone]string{
	VocabularyToneNeedsYou:    "!",
	VocabularyToneWaitingThem: "↗",
	VocabularyToneProgressing: "→",
	VocabularyToneSettled:     "✓",
	VocabularyToneBroken:      "×",
	VocabularyToneUnknown:     "?",
}

// ToneCues returns the closed tone→cue table. The returned map is fresh so
// callers cannot mutate the package-owned table — the same discipline
// DashboardVocabulary and the enumerators below already carry.
func ToneCues() map[VocabularyTone]string {
	fresh := make(map[VocabularyTone]string, len(toneCues))
	for tone, cue := range toneCues {
		fresh[tone] = cue
	}
	return fresh
}

// LiveTransport constants enumerate the client-local states of the live
// dashboard refresh protocol.
const (
	LiveTransportUpdated        = "updated"
	LiveTransportNewerAvailable = "newer-available"
	LiveTransportRefreshing     = "refreshing"
	LiveTransportStale          = "stale"
	LiveTransportUnavailable    = "unavailable"
)

// VocabularyFamilies returns the closed dashboard family set in stable order.
// The returned slice is fresh so callers cannot mutate the vocabulary.
func VocabularyFamilies() []VocabularyFamily {
	return []VocabularyFamily{
		VocabularyFamilyFreshness,
		VocabularyFamilySourceFreshness,
		VocabularyFamilyOutcome,
		VocabularyFamilyLifecycleState,
		VocabularyFamilyTransition,
		VocabularyFamilyReason,
		VocabularyFamilyGate,
		VocabularyFamilyWorkMode,
		VocabularyFamilyDependencyDrift,
		VocabularyFamilyConsistencySeverity,
		VocabularyFamilyOperationalState,
		VocabularyFamilyLiveTransport,
	}
}

// LiveTransportStates returns every client-local transport state in stable
// order. The returned slice is fresh so callers cannot mutate the vocabulary.
func LiveTransportStates() []string {
	return []string{
		LiveTransportUpdated,
		LiveTransportNewerAvailable,
		LiveTransportRefreshing,
		LiveTransportStale,
		LiveTransportUnavailable,
	}
}

var dashboardVocabularyEntries = []VocabularyEntry{
	vocabularyEntry(VocabularyFamilyFreshness, "local-current", "наблюдается локально", "observed locally", "Есть действующая локальная запись о работе, которая ещё не опубликована в общий репозиторий.", "A valid local work record exists and has not yet been published to the shared repository.", VocabularyToneProgressing),
	vocabularyEntry(VocabularyFamilyFreshness, "committed-current", "зафиксировано в Git", "committed in Git", "Последний отчёт о работе сохранён в общей истории и ещё актуален.", "The latest work report is stored in shared history and is still current.", VocabularyToneProgressing),
	vocabularyEntry(VocabularyFamilyFreshness, "stale", "отчёт устарел", "report expired", "Срок актуальности последнего отчёта истёк, поэтому текущую работу нужно перепроверить.", "The latest report has expired, so the current work needs to be checked again.", VocabularyToneBroken),
	vocabularyEntry(VocabularyFamilyFreshness, "finished", "автор сообщил о завершении", "reported finished", "Автор отметил работу завершённой; продолжение по этому отчёту не ожидается.", "The author marked the work complete; no continuation is expected from this report.", VocabularyToneSettled),
	vocabularyEntry(VocabularyFamilyFreshness, "pending-recovery", "публикация требует восстановления", "publication needs recovery", "Локальная работа закрывается, но её публикация не завершена и требует восстановления.", "Local work is closing, but its publication is incomplete and needs recovery.", VocabularyToneNeedsYou),
	vocabularyEntry(VocabularyFamilyFreshness, "unknown", "актуальность неизвестна", "freshness unknown", "Данных недостаточно, чтобы определить, действует ли последний отчёт о работе.", "There is not enough evidence to tell whether the latest work report is current.", VocabularyToneUnknown),

	vocabularyEntry(VocabularyFamilySourceFreshness, "current", "Источник актуален", "Source is current", "Локальная копия источника успешно обновлена и соответствует последнему наблюдению.", "The local source copy refreshed successfully and matches the latest observation.", VocabularyToneSettled),
	vocabularyEntry(VocabularyFamilySourceFreshness, "stale", "Источник устарел", "Source is stale", "Источник прочитан из старой копии, поэтому его данные могут отставать.", "The source was read from an old copy, so its data may lag behind.", VocabularyToneBroken),
	vocabularyEntry(VocabularyFamilySourceFreshness, "unavailable", "Источник недоступен", "Source unavailable", "Источник не удалось прочитать; его факты не входят в текущую картину.", "The source could not be read, so its facts are absent from the current picture.", VocabularyToneBroken),
	vocabularyEntry(VocabularyFamilySourceFreshness, "degraded", "Источник прочитан не полностью", "Source degraded", "Обновление завершилось с явной проблемой, поэтому часть фактов может быть неполной.", "The refresh completed with an explicit problem, so some facts may be incomplete.", VocabularyToneBroken),

	vocabularyEntry(VocabularyFamilyOutcome, "open", "Ещё открыт", "Still open", "Обмен продолжается, и следующий допустимый шаг ещё ожидается.", "The exchange is ongoing and another permitted step is still expected.", VocabularyToneProgressing),
	vocabularyEntry(VocabularyFamilyOutcome, "refused", "Получен отказ", "Refused", "Запрошенный результат отклонён и не считается выполненным.", "The requested result was declined and is not considered fulfilled.", VocabularyToneBroken),
	vocabularyEntry(VocabularyFamilyOutcome, "settled", "Вопрос закрыт", "Resolved", "Обязательство завершено успешно, дальнейший шаг не ожидается.", "The obligation concluded successfully and no further step is expected.", VocabularyToneSettled),
	vocabularyEntry(VocabularyFamilyOutcome, "superseded", "Заменён новым", "Replaced", "Этот документ больше не действует, потому что его заменил другой.", "This document no longer governs because another one replaced it.", VocabularyToneSettled),
	vocabularyEntry(VocabularyFamilyOutcome, "withdrawn", "Отозван автором", "Withdrawn by author", "Автор прекратил этот обмен до завершения запрошенного результата.", "The author ended this exchange before the requested result was completed.", VocabularyToneSettled),

	vocabularyEntry(VocabularyFamilyLifecycleState, "accepted", "Принято к работе", "Accepted for work", "Получатель принял передачу или запрос и может продолжать выполнение.", "The recipient accepted the handoff or request and can continue the work.", VocabularyToneProgressing),
	vocabularyEntry(VocabularyFamilyLifecycleState, "acknowledged", "Получение подтверждено", "Receipt acknowledged", "Адресат подтвердил, что увидел документ; само обязательство может оставаться открытым.", "The recipient confirmed seeing the document; the obligation may still remain open.", VocabularyToneProgressing),
	vocabularyEntry(VocabularyFamilyLifecycleState, "approved", "Согласовано человеком", "Human approved", "Решение прошло обязательное согласование и считается принятым.", "The decision passed its required human approval and is accepted.", VocabularyToneSettled),
	vocabularyEntry(VocabularyFamilyLifecycleState, "blocked", "Работа заблокирована", "Work blocked", "Продолжение невозможно, пока не будет снята указанная блокировка.", "Progress cannot continue until the recorded blocker is removed.", VocabularyToneBroken),
	vocabularyEntry(VocabularyFamilyLifecycleState, "cancelled", "Отменено", "Cancelled", "Работа остановлена до завершения и больше не ожидает продолжения.", "The work stopped before completion and no longer awaits continuation.", VocabularyToneSettled),
	vocabularyEntry(VocabularyFamilyLifecycleState, "closed", "Закрыто", "Closed out", "Цепочка завершена и не ожидает следующего перехода.", "The thread is complete and no next transition is expected.", VocabularyToneSettled),
	vocabularyEntry(VocabularyFamilyLifecycleState, "declined", "Отклонено получателем", "Declined by recipient", "Получатель отказался принять запрос или передачу, поэтому ожидаемый результат не достигнут.", "The recipient declined the request or handoff, so the expected result was not achieved.", VocabularyToneBroken),
	vocabularyEntry(VocabularyFamilyLifecycleState, "deprecated", "Устаревает", "Being phased out", "Версия ещё существует, но для новой работы следует выбрать её преемника.", "The version still exists, but new work should use its successor.", VocabularyToneNeedsYou),
	vocabularyEntry(VocabularyFamilyLifecycleState, "disputed", "Оспорено", "Under dispute", "Участник не согласен с результатом; разногласие нужно разрешить явно.", "A participant contests the result; the disagreement must be resolved explicitly.", VocabularyToneBroken),
	vocabularyEntry(VocabularyFamilyLifecycleState, "draft", "Черновик", "Working draft", "Документ готовится и ещё не опубликован как действующее утверждение.", "The document is being prepared and is not yet published as an active statement.", VocabularyToneProgressing),
	vocabularyEntry(VocabularyFamilyLifecycleState, "in_progress", "Выполняется", "In progress now", "Запрос принят, и работа над результатом продолжается.", "The request was accepted and work on the result is continuing.", VocabularyToneProgressing),
	vocabularyEntry(VocabularyFamilyLifecycleState, "proposed", "Предложено", "Proposed for review", "Предложение опубликовано и ждёт решения адресата.", "The proposal is published and awaits the recipient's decision.", VocabularyToneWaitingThem),
	vocabularyEntry(VocabularyFamilyLifecycleState, "published", "Опубликовано", "Published for use", "Документ доступен участникам как действующая версия.", "The document is available to participants as an active version.", VocabularyToneSettled),
	vocabularyEntry(VocabularyFamilyLifecycleState, "rejected", "Не согласовано", "Approval rejected", "Обязательное согласование завершилось отказом, поэтому решение не принято.", "The required approval ended in rejection, so the decision is not accepted.", VocabularyToneBroken),
	vocabularyEntry(VocabularyFamilyLifecycleState, "responded", "Ответ дан", "Response recorded", "Ответ опубликован; исходный запрос может ждать проверки или закрытия.", "A response is published; the original request may still await verification or closure.", VocabularyToneProgressing),
	vocabularyEntry(VocabularyFamilyLifecycleState, "retired", "Выведено из использования", "Retired from use", "Версия окончательно закрыта для использования и должна быть заменена поддерживаемой.", "The version is permanently closed for use and must be replaced by a supported one.", VocabularyToneSettled),
	vocabularyEntry(VocabularyFamilyLifecycleState, "satisfied", "Требование выполнено", "Requirement met", "Участники подтвердили, что требуемое условие выполнено.", "The participants confirmed that the required condition has been met.", VocabularyToneSettled),
	vocabularyEntry(VocabularyFamilyLifecycleState, "submitted", "Передано на рассмотрение", "Submitted for review", "Документ передан адресату и ждёт его следующего шага.", "The document was sent to its recipient and awaits their next step.", VocabularyToneWaitingThem),
	vocabularyEntry(VocabularyFamilyLifecycleState, "superseded", "Заменено следующим", "Superseded by another", "Этот документ сохранён в истории, но его роль теперь выполняет более новый.", "This document remains in history, but a newer one now serves its role.", VocabularyToneSettled),
	vocabularyEntry(VocabularyFamilyLifecycleState, "verified", "Результат проверен", "Result verified", "Получатель проверил результат и подтвердил его соответствие запросу.", "The recipient checked the result and confirmed that it satisfies the request.", VocabularyToneSettled),
	vocabularyEntry(VocabularyFamilyLifecycleState, "withdrawn", "Снято автором", "Taken back by author", "Автор отозвал документ, поэтому дальнейшая работа по нему не ожидается.", "The author withdrew the document, so no further work on it is expected.", VocabularyToneSettled),

	vocabularyEntry(VocabularyFamilyTransition, "accept", "запрос принят", "request accepted", "Получатель принял запрос или передачу; работа может продолжаться.", "The recipient accepted the request or handoff; work can continue.", VocabularyToneProgressing),
	vocabularyEntry(VocabularyFamilyTransition, "acknowledge", "получение подтверждено", "receipt acknowledged", "Адресат подтвердил получение документа; это ещё не означает выполнения обязательства.", "The recipient confirmed receipt of the document; this does not yet mean the obligation is fulfilled.", VocabularyToneProgressing),
	vocabularyEntry(VocabularyFamilyTransition, "activate", "обязательство введено в действие", "obligation activated", "Опубликованное обязательство введено в действие и теперь заявлено доступным.", "The published obligation was brought into operation and is now declared available.", VocabularyToneProgressing),
	vocabularyEntry(VocabularyFamilyTransition, "approve", "согласовано", "approved", "Обязательное согласование человеком завершилось одобрением.", "The required human review concluded with approval.", VocabularyToneSettled),
	vocabularyEntry(VocabularyFamilyTransition, "block", "заблокировано", "blocked", "Продолжение остановлено записанной блокировкой до её явного снятия.", "Progress is stopped by a recorded blocker until it is explicitly removed.", VocabularyToneBroken),
	vocabularyEntry(VocabularyFamilyTransition, "cancel", "отменено", "cancelled", "Работа остановлена до завершения и больше не ожидает продолжения.", "The work stopped before completion and no longer awaits continuation.", VocabularyToneSettled),
	vocabularyEntry(VocabularyFamilyTransition, "close", "закрытие записано", "close recorded", "Цепочка закрыта и больше не ожидает следующего перехода.", "The thread is closed and no longer awaits another transition.", VocabularyToneSettled),
	vocabularyEntry(VocabularyFamilyTransition, "decline", "получатель отказал", "declined", "Получатель отказался принять запрос или передачу; ожидаемый результат не достигнут.", "The recipient declined the request or handoff; the expected result was not achieved.", VocabularyToneBroken),
	vocabularyEntry(VocabularyFamilyTransition, "deprecate", "объявлено устаревающим", "deprecated", "Версия помечена как устаревающая; для новой работы следует выбрать её преемника.", "The version was marked for phase-out; new work should use its successor.", VocabularyToneNeedsYou),
	vocabularyEntry(VocabularyFamilyTransition, "dispute", "оспорено", "disputed", "Участник оспорил результат; разногласие нужно разрешить явно.", "A participant contested the result; the disagreement must be resolved explicitly.", VocabularyToneBroken),
	vocabularyEntry(VocabularyFamilyTransition, "note", "заметка добавлена", "note added", "К цепочке добавлена информационная заметка; сама по себе она не определяет состояние протокола.", "An informational note was added to the thread; by itself it does not determine protocol state.", VocabularyToneProgressing),
	vocabularyEntry(VocabularyFamilyTransition, "propose", "предложено", "proposed", "Предложение опубликовано и теперь ждёт решения адресата.", "A proposal was published and now awaits the recipient's decision.", VocabularyToneWaitingThem),
	vocabularyEntry(VocabularyFamilyTransition, "publish", "опубликовано", "published", "Документ опубликован и доступен участникам как действующая версия.", "The document was published and is available to participants as an active version.", VocabularyToneSettled),
	vocabularyEntry(VocabularyFamilyTransition, "reject", "отклонено", "rejected", "Обязательное согласование завершилось отказом; решение не принято.", "The required approval ended in rejection; the decision was not accepted.", VocabularyToneBroken),
	vocabularyEntry(VocabularyFamilyTransition, "respond", "ответ записан", "response recorded", "Ответ опубликован; исходный запрос может ещё ждать проверки или закрытия.", "A response was published; the original request may still await verification or closure.", VocabularyToneProgressing),
	vocabularyEntry(VocabularyFamilyTransition, "retire", "выведено из использования", "retired", "Версия окончательно выведена из использования и должна быть заменена поддерживаемой.", "The version was permanently retired from use and must be replaced by a supported one.", VocabularyToneSettled),
	vocabularyEntry(VocabularyFamilyTransition, "satisfy", "требование выполнено", "requirement satisfied", "Участники подтвердили, что требуемое условие выполнено.", "The participants confirmed that the required condition has been met.", VocabularyToneSettled),
	vocabularyEntry(VocabularyFamilyTransition, "start", "работа начата", "work started", "Принятая работа перешла к активному выполнению.", "The accepted work moved into active progress.", VocabularyToneProgressing),
	vocabularyEntry(VocabularyFamilyTransition, "submit", "передано на рассмотрение", "submitted for review", "Документ передан адресату и ждёт его следующего шага.", "The document was sent to its recipient and awaits their next step.", VocabularyToneWaitingThem),
	vocabularyEntry(VocabularyFamilyTransition, "supersede", "заменено новым", "superseded", "Другой документ заменил этот; текущая версия сохраняется только в истории.", "Another document replaced this one; the current version remains only in history.", VocabularyToneSettled),
	vocabularyEntry(VocabularyFamilyTransition, "unblock", "разблокировано", "unblocked", "Записанная блокировка снята, и работа может продолжаться.", "The recorded blocker was removed, and work can continue.", VocabularyToneProgressing),
	vocabularyEntry(VocabularyFamilyTransition, "verify", "результат проверен", "result verified", "Получатель проверил результат и подтвердил его соответствие запросу.", "The recipient checked the result and confirmed that it satisfies the request.", VocabularyToneSettled),
	vocabularyEntry(VocabularyFamilyTransition, "verify-fail", "проверка не пройдена", "verification failed", "Проверка завершилась неуспешно; результат не принят.", "Verification failed; the result was not accepted.", VocabularyToneBroken),
	vocabularyEntry(VocabularyFamilyTransition, "verify-pass", "проверка пройдена", "verification passed", "Проверка завершилась успешно; результат принят.", "Verification passed; the result was accepted.", VocabularyToneSettled),
	vocabularyEntry(VocabularyFamilyTransition, "withdraw", "отозвано", "withdrawn", "Автор отозвал документ до завершения ожидаемого результата.", "The author withdrew the document before the expected result was completed.", VocabularyToneSettled),

	vocabularyEntry(VocabularyFamilyReason, "addressed-no-ack", "Нужно подтвердить получение", "Acknowledge receipt", "Документ адресован вам, но подтверждение получения ещё не записано.", "A document is addressed to you, but your acknowledgement has not been recorded.", VocabularyToneNeedsYou),
	vocabularyEntry(VocabularyFamilyReason, "responded-awaiting-verify-close", "Проверьте ответ", "Verify the response", "На ваш запрос ответили; теперь результат нужно проверить или закрыть цепочку.", "Your request has a response; verify the result or close the thread.", VocabularyToneNeedsYou),
	vocabularyEntry(VocabularyFamilyReason, "disputed-toward-me", "Ответьте на спор", "Resolve the dispute", "Результат оспорен в вашу сторону и ждёт вашего решения.", "The result is disputed toward you and awaits your decision.", VocabularyToneNeedsYou),
	vocabularyEntry(VocabularyFamilyReason, "p1-or-blocking-open", "Критичное дело открыто", "Critical item open", "Открытый документ помечен как срочный или буквально блокирует продолжение.", "An open document is urgent or is literally blocking progress.", VocabularyToneNeedsYou),
	vocabularyEntry(VocabularyFamilyReason, "gate-pending-on-me", "Нужно ваше согласование", "Your approval is needed", "Следующий переход требует подтверждённого действия человека и назначен вам.", "The next transition requires authenticated human action assigned to you.", VocabularyToneNeedsYou),
	vocabularyEntry(VocabularyFamilyReason, "activation-owed", "Активируйте обязательство", "Activate the obligation", "После принятия документа требуется явно начать обещанную работу.", "After accepting the document, the promised work must be started explicitly.", VocabularyToneNeedsYou),
	vocabularyEntry(VocabularyFamilyReason, "state-changed-since-cursor", "Появилось новое состояние", "State changed since last read", "После вашей последней отметки в цепочке произошло значимое изменение.", "A meaningful change occurred in the thread after your last recorded position.", VocabularyToneNeedsYou),
	vocabularyEntry(VocabularyFamilyReason, "declined", "Получатель отказал", "Recipient declined", "Адресат отказался принять документ, поэтому результат нужно пересмотреть.", "The recipient declined the document, so the intended result needs reconsideration.", VocabularyToneBroken),
	vocabularyEntry(VocabularyFamilyReason, "disputed", "Результат оспорен", "Result disputed", "Участник заявил о разногласии, которое ещё не разрешено.", "A participant recorded a disagreement that remains unresolved.", VocabularyToneBroken),
	vocabularyEntry(VocabularyFamilyReason, "stale-sla", "Срок реакции истёк", "Response window expired", "Ожидаемая реакция не появилась в установленное окно времени.", "The expected response did not arrive within its defined time window.", VocabularyToneBroken),
	vocabularyEntry(VocabularyFamilyReason, "needed-by-passed", "Нужный срок прошёл", "Needed-by date passed", "Указанная дата потребности уже прошла, а работа не завершена.", "The recorded needed-by date has passed while the work remains unfinished.", VocabularyToneBroken),
	vocabularyEntry(VocabularyFamilyReason, "overdue-on-me", "Просрочено на вас", "Overdue on your side", "Следующий шаг назначен вам и уже вышел за ожидаемый срок.", "The next move belongs to you and is past its expected time.", VocabularyToneNeedsYou),

	vocabularyEntry(VocabularyFamilyGate, "G3", "Подтверждение владельца", "Owner approval gate", "Согласовать или отклонить решение может только подтверждённый человек из списка владельцев.", "Only an authenticated human in the owner list may approve or reject the decision.", VocabularyToneNeedsYou),

	vocabularyEntry(VocabularyFamilyWorkMode, "planning", "Планирует работу", "Planning the work", "Исполнитель уточняет подход и последовательность действий.", "The worker is defining the approach and sequence of actions.", VocabularyToneProgressing),
	vocabularyEntry(VocabularyFamilyWorkMode, "implementing", "Реализует", "Implementing now", "Исполнитель вносит изменения, которые должны дать запрошенный результат.", "The worker is making the changes needed for the requested result.", VocabularyToneProgressing),
	vocabularyEntry(VocabularyFamilyWorkMode, "testing", "Проверяет результат", "Testing the result", "Исполнитель проверяет, что изменения работают и не нарушают договорённости.", "The worker is checking that the changes work and preserve the contract.", VocabularyToneProgressing),
	vocabularyEntry(VocabularyFamilyWorkMode, "reviewing", "Проводит ревью", "Reviewing changes", "Исполнитель оценивает готовый результат перед завершением или передачей.", "The worker is evaluating the completed result before closure or handoff.", VocabularyToneProgressing),
	vocabularyEntry(VocabularyFamilyWorkMode, "waiting", "Ждёт внешнего шага", "Waiting on another party", "Работа не продолжается, пока другой участник или система не сделает следующий шаг.", "Work cannot continue until another participant or system takes the next step.", VocabularyToneWaitingThem),
	vocabularyEntry(VocabularyFamilyWorkMode, "paused", "Поставил на паузу", "Work paused", "Исполнитель временно остановил работу и не сообщает о текущем продвижении.", "The worker temporarily stopped and is not reporting active progress.", VocabularyToneWaitingThem),
	vocabularyEntry(VocabularyFamilyWorkMode, "finished", "Завершил работу", "Work completed", "Исполнитель сообщил, что его часть работы закончена.", "The worker reported that their part of the work is complete.", VocabularyToneSettled),

	vocabularyEntry(VocabularyFamilyDependencyDrift, "current", "Версия совпадает", "Version aligned", "Потребитель закреплён на доступной актуальной линии контракта.", "The consumer is pinned to an available current contract line.", VocabularyToneSettled),
	vocabularyEntry(VocabularyFamilyDependencyDrift, "behind", "Потребитель отстаёт", "Consumer is behind", "У поставщика есть более новая основная линия, чем закреплена у потребителя.", "The provider has a newer major line than the consumer has pinned.", VocabularyToneNeedsYou),
	vocabularyEntry(VocabularyFamilyDependencyDrift, "deprecated", "Линия устаревает", "Line is deprecated", "Закреплённая линия ещё доступна, но поставщик готовит её к выводу.", "The pinned line remains available, but the provider is phasing it out.", VocabularyToneNeedsYou),
	vocabularyEntry(VocabularyFamilyDependencyDrift, "retired", "Линия уже выведена", "Line already retired", "Потребитель закреплён на линии, которую поставщик больше не поддерживает.", "The consumer is pinned to a line the provider no longer supports.", VocabularyToneBroken),
	vocabularyEntry(VocabularyFamilyDependencyDrift, "missing", "Закреплённая версия не найдена", "Pinned version missing", "В доступном реестре нет версии, которую указал потребитель.", "The available registry does not contain the version requested by the consumer.", VocabularyToneBroken),
	vocabularyEntry(VocabularyFamilyDependencyDrift, "dangling", "Поставщик не найден", "Provider not found", "Зависимость указывает на контракт, которого нет среди прочитанных поставщиков.", "The dependency points to a contract absent from the readable providers.", VocabularyToneBroken),

	vocabularyEntry(VocabularyFamilyConsistencySeverity, "warning", "Нужно сверить источники", "Sources need review", "Несколько источников описывают одну работу несовместимым образом; факт показан, но требует проверки.", "Multiple sources describe the same work incompatibly; the fact is shown but needs review.", VocabularyToneNeedsYou),

	vocabularyEntry(VocabularyFamilyOperationalState, "ready", "Готово к использованию", "Ready for use", "Заявленная операционная возможность присутствует и доступна.", "The declared operational capability is present and available.", VocabularyToneSettled),
	vocabularyEntry(VocabularyFamilyOperationalState, "absent", "Заявлено как отсутствующее", "Declared absent", "Производитель явно сообщил, что этой операционной возможности сейчас нет.", "The producer explicitly reported that this operational capability is unavailable.", VocabularyToneBroken),
	vocabularyEntry(VocabularyFamilyOperationalState, "undeclared", "Не заявлено", "Not declared", "Документ ничего не говорит об этой возможности, поэтому её состояние нельзя предполагать.", "The document says nothing about this capability, so its state cannot be inferred.", VocabularyToneUnknown),

	vocabularyEntry(VocabularyFamilyLiveTransport, "updated", "Показана новая версия", "New version applied", "Клиент успешно принял свежий снимок и уже показывает его.", "The client accepted a fresh snapshot and is displaying it now.", VocabularyToneSettled),
	vocabularyEntry(VocabularyFamilyLiveTransport, "newer-available", "Доступны новые данные", "Newer data available", "Сервер сообщил о более новой ревизии; вы сами решаете, когда заменить текущий снимок.", "The server announced a newer revision; you choose when to replace the current snapshot.", VocabularyToneNeedsYou),
	vocabularyEntry(VocabularyFamilyLiveTransport, "refreshing", "Обновляется", "Refresh in progress", "Клиент запрашивает выбранную новую ревизию и пока сохраняет текущий экран.", "The client is requesting the selected newer revision while keeping the current screen.", VocabularyToneProgressing),
	vocabularyEntry(VocabularyFamilyLiveTransport, "stale", "Связь устарела", "Connection is stale", "Последнее живое обновление не удалось; показанный снимок остаётся последним успешно принятым.", "The latest live refresh failed; the displayed snapshot remains the last successfully accepted one.", VocabularyToneBroken),
	vocabularyEntry(VocabularyFamilyLiveTransport, "unavailable", "Живое обновление недоступно", "Live updates unavailable", "Клиент не может получить обновления и продолжает показывать последний принятый снимок.", "The client cannot receive updates and continues to show the last accepted snapshot.", VocabularyToneBroken),
}

// DashboardVocabulary returns the complete bilingual dashboard dictionary.
// Both the entry slice and fallback are rebuilt so callers cannot mutate the
// package-owned table.
func DashboardVocabulary() VocabularyTable {
	entries := append([]VocabularyEntry(nil), dashboardVocabularyEntries...)
	return VocabularyTable{
		Entries: entries,
		Unknown: VocabularyFallback{
			LabelRU:       "Неизвестное значение",
			LabelEN:       "Unknown value",
			ExplanationRU: "Система не знает, что означает это значение, и не подменяет его догадкой.",
			ExplanationEN: "The system does not know what this value means and will not replace it with a guess.",
			Tone:          VocabularyToneUnknown,
			Cue:           toneCues[VocabularyToneUnknown],
		},
	}
}

func vocabularyEntry(family VocabularyFamily, value, labelRU, labelEN, explanationRU, explanationEN string, tone VocabularyTone) VocabularyEntry {
	return VocabularyEntry{
		Value:         value,
		Family:        family,
		LabelRU:       labelRU,
		LabelEN:       labelEN,
		ExplanationRU: explanationRU,
		ExplanationEN: explanationEN,
		Tone:          tone,
		Cue:           toneCues[tone],
	}
}
