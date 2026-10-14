# Settings of CMS that Polygon has no place for (read by the Polygon
# loader of CMS).
from datetime import timedelta

general = {
    "max_submission_number": 50,
    "min_submission_interval": timedelta(seconds=30),
    "feedback_level": "full",
    "score_precision": 2,
}

datasets = {
    "tests": {
        # Polygon groups 0 (sample), 1 and 2.
        "score_type": "GroupMin",
        "score_type_parameters": [[0, 1], [40, 2], [60, 2]],
    },
}
