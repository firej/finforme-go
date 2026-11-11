package main

import (
	"bytes"
	"fmt"
	"image/color"
	"math"
	"strings"

	"gonum.org/v1/plot"
	"gonum.org/v1/plot/font"
	"gonum.org/v1/plot/plotter"
	"gonum.org/v1/plot/text"
	"gonum.org/v1/plot/vg"
	"gonum.org/v1/plot/vg/draw"
	"gonum.org/v1/plot/vg/vgimg"
)

func renderQuoteChart(data quoteChart) ([]byte, error) {
	if len(data.Points) < 2 {
		return nil, fmt.Errorf("для графика нужны хотя бы две котировки")
	}
	ink := color.RGBA{R: 28, G: 43, B: 62, A: 255}
	muted := color.RGBA{R: 100, G: 116, B: 139, A: 255}
	blue := color.RGBA{R: 37, G: 99, B: 235, A: 255}
	gridColor := color.RGBA{R: 228, G: 234, B: 242, A: 255}
	sans := font.Font{Typeface: "Liberation", Variant: "Sans"}
	p := plot.New()
	values := make(plotter.XYs, len(data.Points))
	low, high := math.Inf(1), math.Inf(-1)
	for i, point := range data.Points {
		values[i] = plotter.XY{X: float64(point.Date.Unix()), Y: point.Rate}
		low, high = math.Min(low, point.Rate), math.Max(high, point.Rate)
	}
	line, points, err := plotter.NewLinePoints(values)
	if err != nil {
		return nil, err
	}
	line.Color, line.Width = blue, vg.Points(2)
	points.Color, points.Radius, points.Shape = blue, vg.Points(2.5), draw.CircleGlyph{}
	grid := plotter.NewGrid()
	grid.Vertical.Width = 0
	grid.Horizontal.Color, grid.Horizontal.Width = gridColor, vg.Points(0.6)
	p.Add(grid, line, points)
	padding := (high - low) * 0.15
	if padding == 0 {
		padding = math.Max(high*0.01, 0.01)
	}
	p.Y.Min, p.Y.Max = math.Max(0, low-padding), high+padding
	p.X.Min, p.X.Max = float64(data.From.Unix()), float64(data.To.Unix())
	var ticks plot.ConstantTicks
	count := min(5, data.Request.Days)
	for i := 0; i < count; i++ {
		day := int(math.Round(float64(i*(data.Request.Days-1)) / float64(count-1)))
		date := data.From.AddDate(0, 0, day)
		ticks = append(ticks, plot.Tick{Value: float64(date.Unix()), Label: date.Format("02.01")})
	}
	p.X.Tick.Marker = ticks
	p.Y.Tick.Marker = plot.TickerFunc(func(min, max float64) []plot.Tick {
		ticks := (plot.DefaultTicks{}).Ticks(min, max)
		for i := range ticks {
			ticks[i].Label = strings.ReplaceAll(ticks[i].Label, ".", ",")
		}
		return ticks
	})
	for _, axis := range []*plot.Axis{&p.X, &p.Y} {
		axis.Color, axis.Width = gridColor, vg.Points(0.6)
		axis.Tick.Color, axis.Tick.Length = gridColor, vg.Points(3)
		axis.Tick.Label.Font, axis.Tick.Label.Color = font.From(sans, 10), muted
		axis.Label.TextStyle.Font, axis.Label.TextStyle.Color = font.From(sans, 10), muted
	}
	units := strings.Split(data.Request.Instrument.Code, "/")
	p.Y.Label.Text = units[1] + " за 1 " + units[0]
	const width, height = 8 * vg.Inch, 4.8 * vg.Inch
	raster := vgimg.NewWith(vgimg.UseWH(width, height), vgimg.UseDPI(150))
	canvas := draw.New(raster)
	write := func(x, y vg.Length, size font.Length, c color.Color, align text.XAlignment, s string) {
		canvas.FillText(text.Style{Font: font.From(sans, size), Color: c, XAlign: align, YAlign: draw.YCenter, Handler: plot.DefaultTextHandler}, vg.Point{X: x, Y: y}, s)
	}
	first, last := data.Points[0], data.Points[len(data.Points)-1]
	change := (last.Rate/first.Rate - 1) * 100
	write(24, height-19, 9, muted, draw.XLeft, "FINFORME  /  КОТИРОВКИ")
	write(24, height-47, 24, ink, draw.XLeft, data.Request.Instrument.Code)
	write(24, height-70, 11, muted, draw.XLeft, fmt.Sprintf("%s — %s · %d дней", data.From.Format("02.01"), data.To.Format("02.01.2006"), data.Request.Days))
	write(width-24, height-47, 22, ink, draw.XRight, fmt.Sprintf("%.*f %s", data.Request.Instrument.Decimals, last.Rate, units[1]))
	write(width-24, height-70, 11, blue, draw.XRight, fmt.Sprintf("%+.2f%% · котировка на %s", change, last.Date.Format("02.01")))
	p.Draw(draw.Crop(canvas, 20, -24, 42, -95))
	footer := fmt.Sprintf("%s · данные %s — %s · точек: %d", data.Request.Instrument.SourceName, first.Date.Format("02.01.2006"), last.Date.Format("02.01.2006"), len(data.Points))
	write(24, 17, 9, muted, draw.XLeft, footer)
	var output bytes.Buffer
	_, err = (vgimg.PngCanvas{Canvas: raster}).WriteTo(&output)
	return output.Bytes(), err
}
