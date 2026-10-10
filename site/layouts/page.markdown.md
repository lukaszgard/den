{{- .Title | replaceRE "\n" " " | printf "# %s" }}
{{ partial "den/markdown.html" . }}
