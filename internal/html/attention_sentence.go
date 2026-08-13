package html

import (
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/cache"
)

const unknownAttentionReason cache.ReasonCode = "unknown"

// attentionSentence composes both supported locales from the first normative
// reason and the already-resolved verdict fields. Reasons are appended by the
// domain in normative order; choosing the first preserves that order without a
// second presentation ranking. An ordinary row with no attention reason gets
// the zero value so reasonSentence stays omitted; a non-empty unrecognised
// reason or typed rule identity gets an explicit fallback.
func attentionSentence(item cache.Item) LocalizedText {
	if len(item.Reasons) == 0 {
		return LocalizedText{}
	}
	return composeAttentionSentence(cache.ReasonCode(item.Reasons[0]), item)
}

func composeAttentionSentence(reason cache.ReasonCode, item cache.Item) LocalizedText {
	ownersEN := humanList(item.WaitingOn, "and")
	ownersRU := humanList(item.WaitingOn, "и")
	if ownersEN == "" {
		ownersEN = "no recorded system"
	}
	if ownersRU == "" {
		ownersRU = "система не указана"
	}
	beEN, ownEN := "is", "owns"
	if nonEmptyCount(item.WaitingOn) > 1 {
		beEN, ownEN = "are", "own"
	}
	transitionEN := item.ExpectedTransition
	transitionRU := item.ExpectedTransition
	if transitionEN == "" {
		transitionEN = "the next protocol move"
		transitionRU = "следующий ход протокола"
	}
	if !item.RuleIdentity.Known() {
		// A future table row paired with an older composer is the same
		// compatibility case as a future reason code: stay useful, but do not
		// pretend a known sentence applies to a rule identity it was never
		// reviewed against.
		reason = unknownAttentionReason
	}
	deadlineEN := item.NeededBy
	deadlineRU := item.NeededBy
	if deadlineEN == "" {
		deadlineEN = "the recorded deadline"
		deadlineRU = "записанный срок"
	}
	gateEN := item.HumanGate
	gateRU := item.HumanGate
	if gateEN == "" {
		gateEN = "the recorded human gate"
		gateRU = "записанный человеческий гейт"
	}

	switch reason {
	case cache.ReasonAddressedNoAck:
		return LocalizedText{
			RU: ownersRU + " должны подтвердить получение; ожидаемый ход — " + transitionRU + ".",
			EN: ownersEN + " must acknowledge receipt; the expected move is " + transitionEN + ".",
		}
	case cache.ReasonRespondedAwaitingVerifyClose:
		return LocalizedText{
			RU: "Ответ уже получен; " + ownersRU + " должны проверить его и выполнить " + transitionRU + ".",
			EN: "A response has arrived; " + ownersEN + " must verify it and perform " + transitionEN + ".",
		}
	case cache.ReasonDisputedTowardMe:
		return LocalizedText{
			RU: "Ответ оспорен; " + ownersRU + " должны дать новый ответ ходом " + transitionRU + ".",
			EN: "The response was disputed; " + ownersEN + " must answer again with " + transitionEN + ".",
		}
	case cache.ReasonP1OrBlockingOpen:
		return LocalizedText{
			RU: "Открытый документ помечен p1 или блокирующим; ход " + transitionRU + " ждут от " + ownersRU + ".",
			EN: "This open item is p1 or blocking; " + ownersEN + " " + beEN + " expected to perform " + transitionEN + ".",
		}
	case cache.ReasonGatePendingOnMe:
		return LocalizedText{
			RU: "Ход " + transitionRU + " ждут от " + ownersRU + "; он требует участия человека через " + gateRU + ".",
			EN: ownersEN + " " + beEN + " expected to perform " + transitionEN + "; it requires a human through " + gateEN + ".",
		}
	case cache.ReasonActivationOwed:
		return LocalizedText{
			RU: "Опубликованную версию уже использует потребитель; " + ownersRU + " должны подтвердить её готовность ходом " + transitionRU + ".",
			EN: "A consumer already uses the published version; " + ownersEN + " must attest readiness with " + transitionEN + ".",
		}
	case cache.ReasonStateChangedSinceCursor:
		return LocalizedText{
			RU: "Документ изменился после последнего чтения входящих; следующий ход " + transitionRU + " ждут от " + ownersRU + ".",
			EN: "This item changed since the last inbox read; " + ownersEN + " " + beEN + " expected to perform " + transitionEN + ".",
		}
	case cache.ReasonDeclined:
		return LocalizedText{
			RU: "Получатель отклонил документ; следующий ход протокола за ним не записан.",
			EN: "The recipient declined this item; no next protocol move is recorded for it.",
		}
	case cache.ReasonDisputed:
		return LocalizedText{
			RU: "Результат оспорен; следующий ход " + transitionRU + " ждут от " + ownersRU + ".",
			EN: "The result is disputed; " + ownersEN + " " + beEN + " expected to perform " + transitionEN + ".",
		}
	case cache.ReasonStaleSLA:
		return LocalizedText{
			RU: "В пределах SLA спейса новых событий не было; ход " + transitionRU + " всё ещё ждут от " + ownersRU + ".",
			EN: "No new event arrived within the space SLA; " + ownersEN + " " + beEN + " still expected to perform " + transitionEN + ".",
		}
	case cache.ReasonNeededByPassed:
		return LocalizedText{
			RU: "Срок " + deadlineRU + " прошёл; ход " + transitionRU + " всё ещё ждут от " + ownersRU + ".",
			EN: "The " + deadlineEN + " deadline has passed; " + ownersEN + " " + beEN + " still expected to perform " + transitionEN + ".",
		}
	case cache.ReasonOverdueOnMe:
		return LocalizedText{
			RU: "Работа с вашим сроком " + deadlineRU + " просрочена; ход " + transitionRU + " всё ещё за " + ownersRU + ".",
			EN: "Work due to you on " + deadlineEN + " is overdue; " + ownersEN + " still " + ownEN + " " + transitionEN + ".",
		}
	default:
		return LocalizedText{
			RU: "Для этого документа записано внимание, но его человеческое объяснение пока неизвестно; следующий ход — " + transitionRU + ", ждём от " + ownersRU + ".",
			EN: "This item is marked for attention, but its human explanation is not known yet; the next move is " + transitionEN + " by " + ownersEN + ".",
		}
	}
}

func humanList(values []string, conjunction string) string {
	clean := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" {
			clean = append(clean, value)
		}
	}
	switch len(clean) {
	case 0:
		return ""
	case 1:
		return clean[0]
	default:
		return strings.Join(clean[:len(clean)-1], ", ") + " " + conjunction + " " + clean[len(clean)-1]
	}
}

func nonEmptyCount(values []string) int {
	count := 0
	for _, value := range values {
		if value != "" {
			count++
		}
	}
	return count
}
