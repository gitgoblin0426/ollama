#! /usr/bin/env bash
NUMBEROFCHOICES=4
CHOICES=$(ollama list | awk '{print $1}')
SELECTIONS=()
declare -a SUMS=()

echo "Select $NUMBEROFCHOICES models to compare:"
select ITEM in $CHOICES; do
    if [[ -n $ITEM ]]; then
        echo "You have selected $ITEM"
        SELECTIONS+=("$ITEM")
        ((COUNT++))
        if [[ $COUNT -eq $NUMBEROFCHOICES ]]; then
            break
        fi
    else
        echo "Invalid selection"
    fi
done

for ITEM in "${SELECTIONS[@]}"; do
    echo "--------------------------------------------------------------"
    echo "Loading the model $ITEM into memory"
    ollama run "$ITEM" ""
    echo "--------------------------------------------------------------"
    echo "Running the questions through the model $ITEM"
    COMMAND_OUTPUT=$(ollama run "$ITEM" --verbose < sourcequestions 2>&1| tee /dev/stderr)

    SUM=$(echo "$COMMAND_OUTPUT" | awk '
    /eval duration:/ {
        value = $3
        if (index(value, "ms") > 0) {
            gsub("ms", "", value)
            value /= 1000
        } else {
            gsub("s", "", value)
        }
        sum += value
    }
    END { print sum }')


    SUMS+=("All questions for $ITEM completed in $SUM seconds")
done

echo ""
echo "--------------------------------------------------------------"
echo -e "Sums of eval durations for each run:"
for val in "${SUMS[@]}"; do
    echo "$val"
done

echo "--------------------------------------------------------------"
echo "Comparison complete. Now you can decide"
echo "which model is best."
echo "--------------------------------------------------------------"