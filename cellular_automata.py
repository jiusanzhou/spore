import numpy as np
import matplotlib.pyplot as plt

# Parameters
height, width = 50, 50
iterations = 100

# Initial random state
state = np.random.choice([0, 1], size=(height, width))

# Function to update the state
def update(state):
    new_state = state.copy()
    for i in range(height):
        for j in range(width):
            total = int((state[i, (j-1)%width] + state[i, (j+1)%width] +
                         state[(i-1)%height, j] + state[(i+1)%height, j] +
                         state[(i-1)%height, (j-1)%width] + state[(i-1)%height, (j+1)%width] +
                         state[(i+1)%height, (j-1)%width] + state[(i+1)%height, (j+1)%width]))
            if state[i, j] == 1 and (total < 2 or total > 3):
                new_state[i, j] = 0
            elif state[i, j] == 0 and total == 3:
                new_state[i, j] = 1
    return new_state

# Visualization and simulation
plt.ion()
fig, ax = plt.subplots()

for _ in range(iterations):
    ax.imshow(state, cmap='binary')
    plt.pause(0.1)
    state = update(state)
plt.ioff()
plt.show()
