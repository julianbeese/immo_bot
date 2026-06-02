package contact

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/chromedp/chromedp"
)

// FormField describes one editable control discovered in the live contact form.
// It is the input the FieldMapper reasons over when the static-selector fill
// path fails (e.g. IS24 changed its DOM and our hard-coded selectors miss).
type FormField struct {
	Selector string        `json:"selector"`
	Name     string        `json:"name"`
	Type     string        `json:"type"` // text, email, tel, number, select, textarea, radio, checkbox...
	Label    string        `json:"label"`
	Required bool          `json:"required"`
	Value    string        `json:"value"`
	Options  []FieldOption `json:"options,omitempty"` // populated for <select>
}

// FieldOption is one <option> of a select field.
type FieldOption struct {
	Value string `json:"value"`
	Text  string `json:"text"`
}

// FieldAction is the mapper's decision for a single field: which selector to act
// on, what value to apply, and how.
type FieldAction struct {
	Selector string `json:"selector"`
	Value    string `json:"value"`
	Kind     string `json:"kind"` // type | select | click
}

// FieldMapper maps live form fields + applicant data to concrete fill actions.
// Implemented by an LLM-backed mapper (see messenger.OpenAIFormFiller); kept as
// an interface so contact has no dependency on the OpenAI client.
type FieldMapper interface {
	MapFormFields(ctx context.Context, fields []FormField, profile Profile, message string) ([]FieldAction, error)
}

// fillViaLLM is the fallback fill path. It reads the current form's fields,
// asks the mapper how to fill them, applies the actions, submits, and verifies.
// Assumes the form is already navigated to and visible on browserCtx.
func (s *Submitter) fillViaLLM(ctx context.Context, message string, profile Profile) error {
	fields, err := s.extractFormFields(ctx)
	if err != nil {
		return fmt.Errorf("extract form fields: %w", err)
	}
	if len(fields) == 0 {
		return fmt.Errorf("no form fields found for llm fallback")
	}

	actions, err := s.mapper.MapFormFields(ctx, fields, profile, message)
	if err != nil {
		return fmt.Errorf("map form fields: %w", err)
	}
	if len(actions) == 0 {
		return fmt.Errorf("mapper returned no actions")
	}

	s.applyActions(ctx, actions)

	if err := chromedp.Run(ctx, s.submitForm()); err != nil {
		return fmt.Errorf("submit after llm fill: %w", err)
	}
	return chromedp.Run(ctx,
		chromedp.Sleep(2*time.Second),
		s.ensureSubmitted(),
	)
}

// extractFormFields dumps the contact form's editable controls with enough
// metadata (label, type, options) for the mapper to reason about them.
func (s *Submitter) extractFormFields(ctx context.Context) ([]FormField, error) {
	var raw string
	if err := chromedp.Run(ctx, chromedp.Evaluate(extractFieldsJS, &raw)); err != nil {
		return nil, err
	}
	var fields []FormField
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		return nil, fmt.Errorf("decode extracted fields: %w", err)
	}
	return fields, nil
}

// applyActions performs each mapper action. Individual failures are tolerated
// (a stale selector should not abort the whole fill); ensureSubmitted is the
// final arbiter of success.
func (s *Submitter) applyActions(ctx context.Context, actions []FieldAction) {
	for _, a := range actions {
		if a.Selector == "" {
			continue
		}
		switch a.Kind {
		case "select":
			if a.Value == "" {
				continue
			}
			_ = chromedp.Run(ctx, chromedp.SetValue(a.Selector, a.Value, chromedp.ByQuery))
		case "click":
			_ = chromedp.Run(ctx, chromedp.Click(a.Selector, chromedp.ByQuery))
		default: // "type"
			if a.Value == "" {
				continue
			}
			_ = s.typeWithDelay(ctx, a.Selector, a.Value)
		}
		time.Sleep(s.behavior.ActionPause())
	}
}

// extractFieldsJS walks the contact form and returns a JSON array of FormField.
// Builds a stable querySelector per control (name → id), resolves a human label
// (label[for] → wrapping label → aria-label → placeholder), and for radio/
// checkbox narrows the selector by value so each option is individually clickable.
const extractFieldsJS = `(() => {
  const form = document.querySelector('form[data-qa="contactForm"], .contact-form, #contactForm') || document;
  const els = form.querySelectorAll('input, select, textarea');
  const out = [];
  for (const el of els) {
    const tag = el.tagName.toLowerCase();
    const type = (el.type || tag).toLowerCase();
    if (type === 'hidden' || type === 'submit' || type === 'button') continue;

    let sel = '';
    if (el.name) {
      sel = tag + '[name="' + el.name + '"]';
      if ((type === 'radio' || type === 'checkbox') && el.value) {
        sel += '[value="' + el.value + '"]';
      }
    } else if (el.id) {
      sel = '#' + (window.CSS && CSS.escape ? CSS.escape(el.id) : el.id);
    } else {
      continue;
    }

    let label = '';
    if (el.id) {
      const l = form.querySelector('label[for="' + (window.CSS && CSS.escape ? CSS.escape(el.id) : el.id) + '"]');
      if (l) label = l.innerText;
    }
    if (!label) { const l = el.closest('label'); if (l) label = l.innerText; }
    if (!label) label = el.getAttribute('aria-label') || el.placeholder || '';

    let options = [];
    if (tag === 'select') {
      options = Array.from(el.options).map(o => ({ value: o.value, text: (o.text || '').trim() }));
    }

    out.push({
      selector: sel,
      name: el.name || '',
      type: type,
      label: (label || '').trim().slice(0, 120),
      required: !!el.required,
      value: el.value || '',
      options: options
    });
  }
  return JSON.stringify(out);
})()`
