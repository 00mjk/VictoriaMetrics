package promrelabel

import (
	"encoding/json"
	"fmt"

	"github.com/VictoriaMetrics/VictoriaMetrics/lib/logger"
	"github.com/VictoriaMetrics/VictoriaMetrics/lib/prompbmarshal"
	"github.com/VictoriaMetrics/VictoriaMetrics/lib/regexutil"
	"github.com/VictoriaMetrics/metricsql"
)

// IfExpression represents `if` expression at RelabelConfig.
//
// The `if` expression can contain arbitrary PromQL-like label filters such as `metric_name{filters...}`
type IfExpression struct {
	s    string
	lfss [][]*labelFilter
}

// String returns string representation of ie.
func (ie *IfExpression) String() string {
	if ie == nil {
		return ""
	}
	return ie.s
}

// Parse parses `if` expression from s and stores it to ie.
func (ie *IfExpression) Parse(s string) error {
	expr, err := metricsql.Parse(s)
	if err != nil {
		return err
	}
	me, ok := expr.(*metricsql.MetricExpr)
	if !ok {
		return fmt.Errorf("expecting series selector; got %q", expr.AppendString(nil))
	}
	lfss, err := metricExprToLabelFilterss(me)
	if err != nil {
		return fmt.Errorf("cannot parse series selector: %w", err)
	}
	ie.s = s
	ie.lfss = lfss
	return nil
}

// UnmarshalJSON unmarshals ie from JSON data.
func (ie *IfExpression) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	return ie.Parse(s)
}

// MarshalJSON marshals ie to JSON.
func (ie *IfExpression) MarshalJSON() ([]byte, error) {
	return json.Marshal(ie.s)
}

// UnmarshalYAML unmarshals ie from YAML passed to f.
func (ie *IfExpression) UnmarshalYAML(f func(interface{}) error) error {
	var s string
	if err := f(&s); err != nil {
		return fmt.Errorf("cannot unmarshal `if` option: %w", err)
	}
	if err := ie.Parse(s); err != nil {
		return fmt.Errorf("cannot parse `if` series selector: %w", err)
	}
	return nil
}

// MarshalYAML marshals ie to YAML.
func (ie *IfExpression) MarshalYAML() (interface{}, error) {
	return ie.s, nil
}

// Match returns true if ie matches the given labels.
func (ie *IfExpression) Match(labels []prompbmarshal.Label) bool {
	if ie == nil {
		return true
	}
	for _, lfs := range ie.lfss {
		if matchLabelFilters(lfs, labels) {
			return true
		}
	}
	return false
}

func matchLabelFilters(lfs []*labelFilter, labels []prompbmarshal.Label) bool {
	for _, lf := range lfs {
		if !lf.match(labels) {
			return false
		}
	}
	return true
}

func metricExprToLabelFilterss(me *metricsql.MetricExpr) ([][]*labelFilter, error) {
	lfssNew := make([][]*labelFilter, len(me.LabelFilterss))
	for i, lfs := range me.LabelFilterss {
		lfsNew := make([]*labelFilter, len(lfs))
		for j := range lfs {
			lf, err := newLabelFilter(&lfs[j])
			if err != nil {
				return nil, fmt.Errorf("cannot parse %s: %w", me.AppendString(nil), err)
			}
			lfsNew[j] = lf
		}
		lfssNew[i] = lfsNew
	}
	return lfssNew, nil
}

// labelFilter contains PromQL filter for `{label op "value"}`
type labelFilter struct {
	label string
	op    string
	value string

	// re contains compiled regexp for `=~` and `!~` op.
	re *regexutil.PromRegex
}

func newLabelFilter(mlf *metricsql.LabelFilter) (*labelFilter, error) {
	lf := &labelFilter{
		label: toCanonicalLabelName(mlf.Label),
		op:    getFilterOp(mlf),
		value: mlf.Value,
	}
	if lf.op == "=~" || lf.op == "!~" {
		re, err := regexutil.NewPromRegex(lf.value)
		if err != nil {
			return nil, fmt.Errorf("cannot parse regexp for %s: %w", mlf.AppendString(nil), err)
		}
		lf.re = re
	}
	return lf, nil
}

func (lf *labelFilter) match(labels []prompbmarshal.Label) bool {
	switch lf.op {
	case "=":
		return lf.equalValue(labels)
	case "!=":
		return !lf.equalValue(labels)
	case "=~":
		return lf.matchRegexp(labels)
	case "!~":
		return !lf.matchRegexp(labels)
	default:
		logger.Panicf("BUG: unexpected operation for label filter: %s", lf.op)
	}
	return false
}

func (lf *labelFilter) equalValue(labels []prompbmarshal.Label) bool {
	labelNameMatches := 0
	for _, label := range labels {
		if toCanonicalLabelName(label.Name) != lf.label {
			continue
		}
		labelNameMatches++
		if label.Value == lf.value {
			return true
		}
	}
	if labelNameMatches == 0 {
		// Special case for {non_existing_label=""}, which matches anything except of non-empty non_existing_label
		return lf.value == ""
	}
	return false
}

func (lf *labelFilter) matchRegexp(labels []prompbmarshal.Label) bool {
	labelNameMatches := 0
	for _, label := range labels {
		if toCanonicalLabelName(label.Name) != lf.label {
			continue
		}
		labelNameMatches++
		if lf.re.MatchString(label.Value) {
			return true
		}
	}
	if labelNameMatches == 0 {
		// Special case for {non_existing_label=~"something|"}, which matches empty non_existing_label
		return lf.re.MatchString("")
	}
	return false
}

func toCanonicalLabelName(labelName string) string {
	if labelName == "__name__" {
		return ""
	}
	return labelName
}

func getFilterOp(mlf *metricsql.LabelFilter) string {
	if mlf.IsNegative {
		if mlf.IsRegexp {
			return "!~"
		}
		return "!="
	}
	if mlf.IsRegexp {
		return "=~"
	}
	return "="
}
